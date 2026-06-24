package localai

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
)

const (
	max3DRemeshBytes       = 512 << 20
	defaultRemeshDetailPct = float32(0.5)
	minRemeshDetailPct     = float32(0.35)
	maxRemeshDetailPct     = float32(2.5)
)

func normalizedRemeshDetail(detail float32) (float32, error) {
	if detail == 0 {
		return defaultRemeshDetailPct, nil
	}
	if math.IsNaN(float64(detail)) || math.IsInf(float64(detail), 0) || detail < minRemeshDetailPct || detail > maxRemeshDetailPct {
		return 0, fmt.Errorf("detail must be between %.2f and %.2f percent", minRemeshDetailPct, maxRemeshDetailPct)
	}
	return detail, nil
}

func saveRemeshUpload(c echo.Context, dir string) (string, error) {
	header, err := c.FormFile("mesh")
	if err != nil {
		return "", fmt.Errorf("mesh is required")
	}
	if header.Size > max3DRemeshBytes {
		return "", fmt.Errorf("mesh exceeds the 512 MiB limit")
	}
	source, err := header.Open()
	if err != nil {
		return "", fmt.Errorf("opening mesh: %w", err)
	}
	defer func() { _ = source.Close() }()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(dir, "remesh-input-*.glb")
	if err != nil {
		return "", err
	}
	path := temp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	written, copyErr := io.Copy(temp, io.LimitReader(source, max3DRemeshBytes+1))
	closeErr := temp.Close()
	if copyErr != nil {
		err = fmt.Errorf("saving mesh: %w", copyErr)
		return "", err
	}
	if closeErr != nil {
		err = closeErr
		return "", err
	}
	if written == 0 {
		err = fmt.Errorf("mesh is empty")
		return "", err
	}
	if written > max3DRemeshBytes {
		err = fmt.Errorf("mesh exceeds the 512 MiB limit")
		return "", err
	}
	return path, nil
}

// Model3DRemeshEndpoint rebuilds an existing generated GLB as a watertight mesh.
// @Summary Applies watertight print remeshing to an existing 3D asset.
// @Tags 3d
// @Accept multipart/form-data
// @Produce model/gltf-binary
// @Param model formData string true "3D model name"
// @Param mesh formData file true "Source GLB"
// @Param detail formData number false "Detail size as percent of the source bounding-box diagonal (0.35–2.5; default 0.5)"
// @Success 200 {file} binary "Remeshed GLB"
// @Router /3d/remesh [post]
func Model3DRemeshEndpoint(ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.Model3DRemeshRequest)
		if !ok || input.Model == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "model is required")
		}
		modelConfig, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || modelConfig == nil {
			return echo.ErrBadRequest
		}
		detail, err := normalizedRemeshDetail(input.Detail)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		source, err := saveRemeshUpload(c, appConfig.GeneratedContentDir)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		defer func() { _ = os.Remove(source) }()

		outputFile, err := os.CreateTemp(appConfig.GeneratedContentDir, "remeshed-*.glb")
		if err != nil {
			return err
		}
		output := outputFile.Name()
		if err := outputFile.Close(); err != nil {
			_ = os.Remove(output)
			return err
		}
		defer func() { _ = os.Remove(output) }()

		if modelConfig.Backend == "" {
			modelConfig.Backend = model.Trellis2CppBackend
		}
		fn, err := backend.Model3DGeneration(
			backend.Model3DGenerationOptions{
				Image:       source,
				Destination: output,
				Params: map[string]string{
					"operation":      "print_remesh",
					"alpha_ratio":    strconv.FormatFloat(float64(detail/100), 'g', -1, 32),
					"detail_percent": strconv.FormatFloat(float64(detail), 'g', -1, 32),
					"texture_size":   "2048",
				},
			},
			ml,
			*modelConfig,
			appConfig,
		)
		if err != nil {
			return mapBackendError(err)
		}
		if err := fn(); err != nil {
			return mapBackendError(err)
		}

		file, err := os.Open(filepath.Clean(output))
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="remeshed.glb"`)
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.Stream(http.StatusOK, "model/gltf-binary", file)
	}
}
