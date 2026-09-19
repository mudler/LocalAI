package openai

import (
	"encoding/base64"
	"encoding/json"
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

		// Create JSON with base64 fields expected by backend
		b64Image := base64.StdEncoding.EncodeToString(imgBytes)
		b64Mask := base64.StdEncoding.EncodeToString(maskBytes)

		// get model config from context (middleware set it)
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			xlog.Error("Inpainting Endpoint - model config not found in context")
			return echo.ErrBadRequest
		}

		publicDir := filepath.Join(appConfig.GeneratedContentDir, "images")
		if err := os.MkdirAll(publicDir, 0750); err != nil {
			xlog.Error("Inpainting Endpoint - failed to create generated content dir", "error", err, "dir", publicDir)
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to prepare storage")
		}

		// Inputs can contain private user data. Stage them outside the directory
		// mounted at /generated-images and remove the entire private workspace on
		// every exit path. The generated result is copied into the public tree
		// only after the backend completes successfully.
		stagingDir, err := os.MkdirTemp("", "localai-inpainting-*")
		if err != nil {
			return err
		}
		defer func() {
			if err := os.RemoveAll(stagingDir); err != nil {
				xlog.Warn("Inpainting Endpoint - failed to remove private staging dir", "error", err, "dir", stagingDir)
			}
		}()

		id := uuid.New().String()
		jsonFile := map[string]string{
			"image":      b64Image,
			"mask_image": b64Mask,
		}
		jf, err := os.CreateTemp(stagingDir, "inpaint-*.json")
		if err != nil {
			return err
		}
		jsonPath := jf.Name()

		// write original image and mask to disk as ref images so backends that
		// accept reference image files can use them (maintainer request).
		origRef := filepath.Join(stagingDir, "reference-image")
		if err := os.WriteFile(origRef, imgBytes, 0600); err != nil {
			return err
		}
		maskRef := filepath.Join(stagingDir, "reference-mask")
		if err := os.WriteFile(maskRef, maskBytes, 0600); err != nil {
			return err
		}

		// write JSON
		enc := json.NewEncoder(jf)
		if err := enc.Encode(jsonFile); err != nil {
			_ = jf.Close()
			return err
		}
		if err := jf.Close(); err != nil {
			return err
		}
		dst := filepath.Join(stagingDir, "generated.png")

		// Determine width/height default
		width := 512
		height := 512

		// Call backend image generation via indirection so tests can stub it
		// Note: ImageGenerationFunc will call into the loaded model's GenerateImage which expects src JSON
		// Also pass ref images (orig + mask) so backends that support ref images can use them.
		refImages := []string{origRef, maskRef}
		fn, err := backend.ImageGenerationFunc(c.Request().Context(), height, width, steps, 0, prompt, "", jsonPath, dst, ml, *cfg, appConfig, refImages)
		if err != nil {
			return err
		}

		// Execute generation function (blocking)
		if err := fn(); err != nil {
			return err
		}

		output, err := os.Open(filepath.Clean(dst))
		if err != nil {
			return err
		}
		publicTemp, err := os.CreateTemp(publicDir, ".inpaint-output-*.png")
		if err != nil {
			_ = output.Close()
			return err
		}
		publicTempPath := publicTemp.Name()
		defer func() { _ = os.Remove(publicTempPath) }()
		if _, err := io.Copy(publicTemp, output); err != nil {
			_ = output.Close()
			_ = publicTemp.Close()
			return err
		}
		if err := output.Close(); err != nil {
			_ = publicTemp.Close()
			return err
		}
		if err := publicTemp.Close(); err != nil {
			return err
		}
		publishedPath := filepath.Join(publicDir, "inpaint_"+id+".png")
		if err := os.Rename(publicTempPath, publishedPath); err != nil {
			return err
		}
		keepPublished := false
		defer func() {
			if !keepPublished {
				_ = os.Remove(publishedPath)
			}
		}()

		// On success, build response URL using BaseURL middleware helper and
		// the same `generated-images` prefix used by the server static mount.
		baseURL := middleware.BaseURL(c)

		// Build response using url.JoinPath for correct URL escaping
		imgPath, err := url.JoinPath(baseURL, "generated-images", filepath.Base(publishedPath))
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

		if err := c.JSON(http.StatusOK, resp); err != nil {
			return err
		}
		keepPublished = true
		return nil
	}
}
