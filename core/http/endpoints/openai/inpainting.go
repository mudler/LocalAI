package openai

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
)

// enableParametersWithMask returns the enable_parameters option with the mask
// of this request. Any mask the model configuration names is replaced: the
// request decides which area is repainted.
func enableParametersWithMask(existing, maskPath string) string {
	var kept []string
	for _, p := range strings.Split(existing, ",") {
		if p = strings.TrimSpace(p); p != "" && !strings.HasPrefix(p, "mask:") {
			kept = append(kept, p)
		}
	}
	return strings.Join(append(kept, "mask:"+maskPath), ",")
}

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
			xlog.Error("Inpainting Endpoint - missing model or prompt")
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
			xlog.Error("Inpainting Endpoint - missing image file", "error", err)
			return echo.NewHTTPError(http.StatusBadRequest, "missing image file")
		}
		maskFile, err := c.FormFile("mask")
		if err != nil {
			xlog.Error("Inpainting Endpoint - missing mask file", "error", err)
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

		// get model config from context (middleware set it)
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			xlog.Error("Inpainting Endpoint - model config not found in context")
			return echo.ErrBadRequest
		}

		// Use the GeneratedContentDir so the generated PNG is placed where the
		// HTTP static handler serves `/generated-images`.
		tmpDir := appConfig.GeneratedContentDir
		// Ensure the directory exists
		if err := os.MkdirAll(tmpDir, 0750); err != nil {
			xlog.Error("Inpainting Endpoint - failed to create generated content dir", "error", err, "dir", tmpDir)
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to prepare storage")
		}
		id := uuid.New().String()
		// setup cleanup on error; if everything succeeds we set success = true
		success := false
		var dst string
		var origRef string
		var maskRef string
		defer func() {
			// The input files are of no use once the call is over, whether it
			// worked or not. The result stays only on success.
			for _, name := range []string{origRef, maskRef} {
				if name != "" {
					if rerr := os.Remove(name); rerr != nil && !os.IsNotExist(rerr) {
						xlog.Warn("Inpainting Endpoint - failed to remove input file", "error", rerr, "file", name)
					}
				}
			}
			if !success && dst != "" {
				if rerr := os.Remove(dst); rerr != nil && !os.IsNotExist(rerr) {
					xlog.Warn("Inpainting Endpoint - failed to remove dst file in cleanup", "error", rerr, "file", dst)
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
			xlog.Warn("Inpainting Endpoint - failed to close orig temp file", "error", cerr)
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
			xlog.Warn("Inpainting Endpoint - failed to close mask temp file", "error", cerr)
		}
		maskRef = maskTmp.Name()
		// prepare dst
		outTmp, err := os.CreateTemp(tmpDir, "out_")
		if err != nil {
			return err
		}
		if cerr := outTmp.Close(); cerr != nil {
			xlog.Warn("Inpainting Endpoint - failed to close out temp file", "error", cerr)
		}
		dst = outTmp.Name() + ".png"
		if err := os.Rename(outTmp.Name(), dst); err != nil {
			return err
		}

		// Determine width/height default
		width := 512
		height := 512

		// Call backend image generation via indirection so tests can stub it.
		// The source is the plain image file: the backends load src as an
		// image, and none of them reads the JSON file this handler used to
		// write, so stablediffusion-ggml failed to load it. The mask reaches
		// stablediffusion-ggml through the enable_parameters option, the only
		// channel that backend reads a mask from. Both files also go as ref
		// images, so backends that take references can use them.
		refImages := []string{origRef, maskRef}
		withMask := *cfg
		withMask.Diffusers.EnableParameters = enableParametersWithMask(cfg.Diffusers.EnableParameters, maskRef)
		fn, err := backend.ImageGenerationFunc(c.Request().Context(), height, width, steps, 0, prompt, "", origRef, dst, ml, withMask, appConfig, refImages)
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
			Usage: &schema.OpenAIUsage{
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      0,
				InputTokens:      0,
				OutputTokens:     0,
				InputTokensDetails: &schema.InputTokensDetails{
					TextTokens:  0,
					ImageTokens: 0,
				},
			},
		}

		// mark success so defer cleanup will not remove output files
		success = true

		return c.JSON(http.StatusOK, resp)
	}
}
