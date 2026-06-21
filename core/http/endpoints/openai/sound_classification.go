package openai

import (
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"
)

// SoundClassificationEndpoint runs an audio-tagging / sound-event
// classification model (e.g. ced) over an uploaded clip and returns the
// scored AudioSet tags in score-descending order. It mirrors the
// transcription path: multipart audio upload -> temp file -> backend call.
//
// @Summary Classify sound events in audio (audio tagging).
// @Tags audio
// @accept multipart/form-data
// @Param model formData string true "model"
// @Param file formData file true "audio file"
// @Param top_k formData int false "number of top tags to return (0 = backend default)"
// @Param threshold formData number false "drop tags scoring below this value"
// @Success 200 {object} schema.SoundClassificationResult
// @Router /v1/audio/classification [post]
func SoundClassificationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		modelConfig, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || modelConfig == nil {
			return echo.ErrBadRequest
		}

		req := backend.SoundDetectionRequest{
			TopK:      int32(parseFormInt(c, "top_k", 0)),
			Threshold: float32(parseFormFloat(c, "threshold", 0)),
		}

		file, err := c.FormFile("file")
		if err != nil {
			return err
		}
		f, err := file.Open()
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		dir, err := os.MkdirTemp("", "sound-classification")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()

		dst := filepath.Join(dir, path.Base(file.Filename))
		dstFile, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dstFile, f); err != nil {
			xlog.Debug("Audio file copying error", "filename", file.Filename, "dst", dst, "error", err)
			_ = dstFile.Close()
			return err
		}
		_ = dstFile.Close()
		req.Audio = dst

		result, err := backend.ModelSoundDetection(c.Request().Context(), req, ml, *modelConfig, appConfig)
		if err != nil {
			xlog.Error("Sound classification failed",
				"model", modelConfig.Name,
				"audio", dst,
				"error", err)
			return err
		}

		return c.JSON(http.StatusOK, result)
	}
}
