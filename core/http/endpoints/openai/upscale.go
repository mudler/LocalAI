package openai

import (
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
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
)

// UpscaleEndpoint handles POST /v1/images/upscale
//
// @Summary      Image upscaling
// @Description  Upscale an image using a specified model (e.g. realesrgan). Accepts multipart/form-data.
// @Tags         images
// @Accept       multipart/form-data
// @Produce      application/json
// @Param        model   formData  string  true   "Upscaler model identifier (e.g. realesrgan)"
// @Param        image   formData  file    true   "Input image file"
// @Param        scale   formData  int     false  "Upscale factor: 2 or 4 (default 2)"
// @Success      200 {object} schema.OpenAIResponse
// @Failure      400 {object} map[string]string
// @Failure      500 {object} map[string]string
// @Router       /v1/images/upscale [post]
func UpscaleEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.FormValue("model")
		scaleStr := c.FormValue("scale")

		if modelName == "" {
			xlog.Error("Upscale Endpoint - missing model")
			return echo.NewHTTPError(http.StatusBadRequest, "missing model")
		}

		scale := 2
		if scaleStr != "" {
			if v, err := strconv.Atoi(scaleStr); err == nil && (v == 2 || v == 4) {
				scale = v
			}
		}

		// Read uploaded image
		imageFile, err := c.FormFile("image")
		if err != nil {
			xlog.Error("Upscale Endpoint - missing image file", "error", err)
			return echo.NewHTTPError(http.StatusBadRequest, "missing image file")
		}

		imgSrc, err := imageFile.Open()
		if err != nil {
			return err
		}
		defer imgSrc.Close()
		imgBytes, err := io.ReadAll(imgSrc)
		if err != nil {
			return err
		}

		// Get model config from middleware context
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			xlog.Error("Upscale Endpoint - model config not found in context")
			return echo.ErrBadRequest
		}

		tmpDir := appConfig.GeneratedContentDir
		if err := os.MkdirAll(tmpDir, 0750); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to prepare storage")
		}

		// Write input image to a temp file
		srcTmp, err := os.CreateTemp(tmpDir, "upscale_src_")
		if err != nil {
			return err
		}
		if _, err := srcTmp.Write(imgBytes); err != nil {
			_ = srcTmp.Close()
			_ = os.Remove(srcTmp.Name())
			return err
		}
		if err := srcTmp.Close(); err != nil {
			xlog.Warn("Upscale Endpoint - failed to close src temp file", "error", err)
		}
		srcPath := srcTmp.Name()
		defer os.Remove(srcPath)

		// Prepare output file path
		id := uuid.New().String()
		dstPath := filepath.Join(tmpDir, fmt.Sprintf("upscale_%s.png", id))
		defer func() {
			// Only remove on error; success path keeps the file for serving
		}()

		fn, err := backend.ImageUpscaleFunc(c.Request().Context(), srcPath, dstPath, scale, ml, *cfg, appConfig)
		if err != nil {
			return err
		}
		if err := fn(); err != nil {
			_ = os.Remove(dstPath)
			return err
		}

		baseURL := middleware.BaseURL(c)
		imgURL, err := url.JoinPath(baseURL, "generated-images", filepath.Base(dstPath))
		if err != nil {
			_ = os.Remove(dstPath)
			return err
		}

		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Data:    []schema.Item{{URL: imgURL}},
			Usage: &schema.OpenAIUsage{
				InputTokensDetails: &schema.InputTokensDetails{},
			},
		}

		return c.JSON(http.StatusOK, resp)
	}
}
