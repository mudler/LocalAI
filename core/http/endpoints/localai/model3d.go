package localai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/xlog"

	model "github.com/mudler/LocalAI/pkg/model"
)

// Conditioning images are single frames, so a much tighter cap than the
// video-input limit is enough.
const max3DInputBytes = 32 << 20

var (
	valid3DQualities   = []string{"", "auto", "coarse", "512", "1024"}
	valid3DBackgrounds = []string{"", "auto", "keep", "black", "white"}
)

// Model3DEndpoint
// @Summary Creates a 3D asset (binary glTF / GLB) from a conditioning image.
// @Tags 3d
// @Param request body schema.Model3DRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/3d/generations [post]
func Model3DEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.Model3DRequest)
		if !ok || input.Model == "" {
			xlog.Error("3D Endpoint - Invalid Input")
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			xlog.Error("3D Endpoint - Invalid Config")
			return echo.ErrBadRequest
		}

		if input.Image == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "image is required: 3D generation is image-conditioned")
		}
		// Reject unknown enum values here rather than surfacing an opaque
		// backend error after a model load.
		if !slices.Contains(valid3DQualities, input.Quality) {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid quality %q: must be one of auto, coarse, 512, 1024", input.Quality))
		}
		if !slices.Contains(valid3DBackgrounds, input.Background) {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid background %q: must be one of auto, keep, black, white", input.Background))
		}

		src, err := stageVideoMediaWithLimit(c.Request().Context(), appConfig.GeneratedContentDir, input.Image, max3DInputBytes)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid image: %v", err))
		}
		defer func() { _ = os.Remove(src) }()

		xlog.Debug("Parameter Config", "config", config)

		if config.Backend == "" {
			config.Backend = model.Trellis2CppBackend
		}

		step := input.Step
		if step == 0 && config.Step != 0 {
			step = int32(config.Step)
		}
		cfgScale := input.CFGScale
		if cfgScale == 0 && config.CFGScale != 0 {
			cfgScale = config.CFGScale
		}

		b64JSON := input.ResponseFormat == "b64_json"

		tempDir := ""
		if !b64JSON {
			tempDir = filepath.Join(appConfig.GeneratedContentDir, "3d")
			if err := os.MkdirAll(tempDir, 0o750); err != nil {
				return err
			}
		}
		// Create a temporary file
		outputFile, err := os.CreateTemp(tempDir, "b64")
		if err != nil {
			return err
		}
		if err := outputFile.Close(); err != nil {
			_ = os.Remove(outputFile.Name())
			return err
		}

		output := outputFile.Name() + ".glb"

		// Rename the temporary file
		err = os.Rename(outputFile.Name(), output)
		if err != nil {
			_ = os.Remove(outputFile.Name())
			return err
		}
		preserveOutput := false
		defer func() {
			if !preserveOutput {
				_ = os.Remove(output)
			}
		}()

		baseURL := middleware.BaseURL(c)

		xlog.Debug("Model3DEndpoint: Calling Model3DGeneration",
			"quality", input.Quality,
			"background", input.Background,
			"cfg_scale", cfgScale,
			"step", step,
			"texture_steps", input.TextureSteps,
			"seed", input.Seed)

		fn, err := backend.Model3DGeneration(
			backend.Model3DGenerationOptions{
				Image:        src,
				Destination:  output,
				Seed:         input.Seed,
				Step:         step,
				CFGScale:     cfgScale,
				TextureSteps: input.TextureSteps,
				Quality:      input.Quality,
				Background:   input.Background,
				Params:       input.Params,
			},
			ml,
			*config,
			appConfig,
		)
		if err != nil {
			return mapBackendError(err)
		}
		if err := fn(); err != nil {
			return mapBackendError(err)
		}

		item := &schema.Item{}

		if b64JSON {
			data, err := os.ReadFile(output)
			if err != nil {
				return err
			}
			item.B64JSON = base64.StdEncoding.EncodeToString(data)
		} else {
			base := filepath.Base(output)
			item.URL, err = url.JoinPath(baseURL, "generated-3d", base)
			if err != nil {
				return err
			}
			preserveOutput = true
		}

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Data:    []schema.Item{*item},
		}

		jsonResult, _ := json.Marshal(resp)
		xlog.Debug("Response", "response", string(jsonResult))

		return c.JSON(200, resp)
	}
}
