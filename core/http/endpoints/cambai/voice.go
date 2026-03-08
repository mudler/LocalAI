package cambai

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// ListVoicesEndpoint handles CAMB AI list voices (GET /apis/list-voices).
func ListVoicesEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		ttsConfigs := cl.GetModelConfigsByFilter(config.BuildUsecaseFilterFn(config.FLAG_TTS))

		voices := make([]schema.CambAIVoice, 0)
		for i, cfg := range ttsConfigs {
			voice := schema.CambAIVoice{
				ID:   i + 1,
				Name: cfg.Name,
			}
			if cfg.Voice != "" {
				voice.Name = fmt.Sprintf("%s (%s)", cfg.Name, cfg.Voice)
			}
			voices = append(voices, voice)
		}

		return c.JSON(http.StatusOK, voices)
	}
}

// CreateCustomVoiceEndpoint handles CAMB AI custom voice creation (POST /apis/create-custom-voice).
// Accepts an audio file upload and saves it for voice cloning.
func CreateCustomVoiceEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		voiceName := c.FormValue("voice_name")
		if voiceName == "" {
			return c.JSON(http.StatusBadRequest, schema.CambAIErrorResponse{
				Detail: "voice_name is required",
			})
		}

		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, schema.CambAIErrorResponse{
				Detail: "Audio file is required. Upload as multipart form field 'file'.",
			})
		}

		f, err := file.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		// Save audio file to models directory for voice cloning
		voiceDir := filepath.Join(ml.ModelPath, "voices")
		if err := os.MkdirAll(voiceDir, 0750); err != nil {
			return err
		}

		ext := filepath.Ext(file.Filename)
		if ext == "" {
			ext = ".wav"
		}
		dstPath := filepath.Join(voiceDir, voiceName+ext)

		dst, err := os.Create(dstPath)
		if err != nil {
			return err
		}
		defer dst.Close()

		if _, err := io.Copy(dst, f); err != nil {
			return err
		}

		xlog.Info("Custom voice audio saved", "name", voiceName, "path", dstPath)

		return c.JSON(http.StatusOK, schema.CambAICreateCustomVoiceResponse{
			VoiceID: 0,
		})
	}
}
