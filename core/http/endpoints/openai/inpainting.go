package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
)

// InpaintingEndpoint handles POST /v1/images/inpainting
//
// Swagger / OpenAPI docstring (swaggo):
// @Summary      Image inpainting
// @Description  Perform image inpainting. Accepts multipart/form-data with `image` and `mask` files.
// @Tags         images
// @Accept       multipart/form-data
// @Produce      application/json
// @Param        model   formData  string  true   "Model identifier"
// @Param        prompt  formData  string  true   "Text prompt guiding the generation"
// @Param        steps   formData  int     false  "Number of inference steps (default 25)"
// @Param        image   formData  file    true   "Original image file"
// @Param        mask    formData  file    true   "Mask image file (white = area to inpaint)"
// @Success      200 {object} schema.OpenAIResponse
// @Failure      400 {object} map[string]string
// @Failure      500 {object} map[string]string
// @Router       /v1/images/inpainting [post]
func InpaintingEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Parse basic form values
		modelName := c.FormValue("model")
		prompt := c.FormValue("prompt")
		stepsStr := c.FormValue("steps")

		if modelName == "" || prompt == "" {
			log.Error().Msg("Inpainting Endpoint - missing model or prompt")
			return echo.ErrBadRequest
		}

		// steps default
		steps := 25
		if stepsStr != "" {
			if v, err := strconv.Atoi(stepsStr); err == nil {
				steps = v
			}
		}

		// Get uploaded files
		imageFile, err := c.FormFile("image")
		if err != nil {
			log.Error().Err(err).Msg("Inpainting Endpoint - missing image file")
			return echo.NewHTTPError(http.StatusBadRequest, "missing image file")
		}
		maskFile, err := c.FormFile("mask")
		if err != nil {
			log.Error().Err(err).Msg("Inpainting Endpoint - missing mask file")
			return echo.NewHTTPError(http.StatusBadRequest, "missing mask file")
		}

		// Read files into memory (small files expected)
		imgSrc, err := imageFile.Open()
		if err != nil {
			return err
		}
		defer imgSrc.Close()
		imgBytes, err := io.ReadAll(imgSrc)
		if err != nil {
			return err
		}

		maskSrc, err := maskFile.Open()
		if err != nil {
			return err
		}
		defer maskSrc.Close()
		maskBytes, err := io.ReadAll(maskSrc)
		if err != nil {
			return err
		}

		// Create JSON with base64 fields expected by backend
		b64Image := base64.StdEncoding.EncodeToString(imgBytes)
		b64Mask := base64.StdEncoding.EncodeToString(maskBytes)

		// get model config from context (middleware set it)
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			log.Error().Msg("Inpainting Endpoint - model config not found in context")
			return echo.ErrBadRequest
		}

		// Use the GeneratedContentDir so the generated PNG is placed where the
		// HTTP static handler serves `/generated-images`.
		tmpDir := appConfig.GeneratedContentDir
		// Ensure the directory exists
		if err := os.MkdirAll(tmpDir, 0750); err != nil {
			log.Error().Err(err).Msgf("Inpainting Endpoint - failed to create generated content dir: %s", tmpDir)
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to prepare storage")
		}
		id := uuid.New().String()
		jsonPath := filepath.Join(tmpDir, fmt.Sprintf("inpaint_%s.json", id))
		jsonFile := map[string]string{
			"image":      b64Image,
			"mask_image": b64Mask,
		}
		jf, err := os.CreateTemp(tmpDir, "inpaint_")
		if err != nil {
			return err
		}
		// setup cleanup on error; if everything succeeds we set success = true
		success := false
		var dst string
		var origRef string
		var maskRef string
		defer func() {
			if !success {
				// Best-effort cleanup; log any failures
				if jf != nil {
					if cerr := jf.Close(); cerr != nil {
						log.Warn().Err(cerr).Msg("Inpainting Endpoint - failed to close temp json file in cleanup")
					}
					if name := jf.Name(); name != "" {
						if rerr := os.Remove(name); rerr != nil && !os.IsNotExist(rerr) {
							log.Warn().Err(rerr).Msgf("Inpainting Endpoint - failed to remove temp json file %s in cleanup", name)
						}
					}
				}
				if jsonPath != "" {
					if rerr := os.Remove(jsonPath); rerr != nil && !os.IsNotExist(rerr) {
						log.Warn().Err(rerr).Msgf("Inpainting Endpoint - failed to remove json file %s in cleanup", jsonPath)
					}
				}
				if dst != "" {
					if rerr := os.Remove(dst); rerr != nil && !os.IsNotExist(rerr) {
						log.Warn().Err(rerr).Msgf("Inpainting Endpoint - failed to remove dst file %s in cleanup", dst)
					}
				}
				if origRef != "" {
					if rerr := os.Remove(origRef); rerr != nil && !os.IsNotExist(rerr) {
						log.Warn().Err(rerr).Msgf("Inpainting Endpoint - failed to remove orig ref file %s in cleanup", origRef)
					}
				}
				if maskRef != "" {
					if rerr := os.Remove(maskRef); rerr != nil && !os.IsNotExist(rerr) {
						log.Warn().Err(rerr).Msgf("Inpainting Endpoint - failed to remove mask ref file %s in cleanup", maskRef)
					}
				}
			}
		}()

		// write original image and mask to disk as ref images so backends that
		// accept reference image files can use them (maintainer request).
		origTmp, err := os.CreateTemp(tmpDir, "refimg_")
		if err != nil {
			return err
		}
		if _, err := origTmp.Write(imgBytes); err != nil {
			_ = origTmp.Close()
			_ = os.Remove(origTmp.Name())
			return err
		}
		if cerr := origTmp.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("Inpainting Endpoint - failed to close orig temp file")
		}
		origRef = origTmp.Name()

		maskTmp, err := os.CreateTemp(tmpDir, "refmask_")
		if err != nil {
			// cleanup origTmp on error
			_ = os.Remove(origRef)
			return err
		}
		if _, err := maskTmp.Write(maskBytes); err != nil {
			_ = maskTmp.Close()
			_ = os.Remove(maskTmp.Name())
			_ = os.Remove(origRef)
			return err
		}
		if cerr := maskTmp.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("Inpainting Endpoint - failed to close mask temp file")
		}
		maskRef = maskTmp.Name()
		// write JSON
		enc := json.NewEncoder(jf)
		if err := enc.Encode(jsonFile); err != nil {
			if cerr := jf.Close(); cerr != nil {
				log.Warn().Err(cerr).Msg("Inpainting Endpoint - failed to close temp json file after encode error")
			}
			return err
		}
		if cerr := jf.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("Inpainting Endpoint - failed to close temp json file")
		}
		// rename to desired name
		if err := os.Rename(jf.Name(), jsonPath); err != nil {
			return err
		}
		// prepare dst
		outTmp, err := os.CreateTemp(tmpDir, "out_")
		if err != nil {
			return err
		}
		if cerr := outTmp.Close(); cerr != nil {
			log.Warn().Err(cerr).Msg("Inpainting Endpoint - failed to close out temp file")
		}
		dst = outTmp.Name() + ".png"
		if err := os.Rename(outTmp.Name(), dst); err != nil {
			return err
		}

		// Determine width/height default
		width := 512
		height := 512

		// Call backend image generation via indirection so tests can stub it
		// Note: ImageGenerationFunc will call into the loaded model's GenerateImage which expects src JSON
		// Also pass ref images (orig + mask) so backends that support ref images can use them.
		refImages := []string{origRef, maskRef}
		fn, err := backend.ImageGenerationFunc(height, width, 0, steps, 0, prompt, "", jsonPath, dst, ml, *cfg, appConfig, refImages)
		if err != nil {
			return err
		}

		// Execute generation function (blocking)
		if err := fn(); err != nil {
			return err
		}

		// On success, build response URL using BaseURL middleware helper and
		// the same `generated-images` prefix used by the server static mount.
		baseURL := middleware.BaseURL(c)

		// Build response using url.JoinPath for correct URL escaping
		imgPath, err := url.JoinPath(baseURL, "generated-images", filepath.Base(dst))
		if err != nil {
			return err
		}

		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Data: []schema.Item{{
				URL: imgPath,
			}},
		}

		// mark success so defer cleanup will not remove output files
		success = true

		return c.JSON(http.StatusOK, resp)
	}
}
