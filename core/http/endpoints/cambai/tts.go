package cambai

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// ttsTaskResults stores results of async TTS tasks keyed by task ID.
var ttsTaskResults = sync.Map{}

// TTSStreamEndpoint handles CAMB AI streaming TTS (POST /apis/tts-stream).
func TTSStreamEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.CambAITTSStreamRequest)
		if !ok || input.SpeechModel == "" || input.Text == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("CAMB AI TTS stream request received", "model", input.SpeechModel)

		voice := fmt.Sprintf("%d", input.VoiceID)
		language := input.Language

		c.Response().Header().Set("Content-Type", "audio/wav")
		c.Response().Header().Set("Transfer-Encoding", "chunked")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")

		err := backend.ModelTTSStream(input.Text, voice, language, ml, appConfig, *cfg, func(audioChunk []byte) error {
			_, writeErr := c.Response().Write(audioChunk)
			if writeErr != nil {
				return writeErr
			}
			c.Response().Flush()
			return nil
		})
		if err != nil {
			// Fallback to non-streaming TTS
			xlog.Debug("Streaming TTS not supported, falling back to non-streaming", "error", err)
			filePath, _, ttsErr := backend.ModelTTS(input.Text, voice, language, ml, appConfig, *cfg)
			if ttsErr != nil {
				return ttsErr
			}
			filePath, contentType := audio.NormalizeAudioFile(filePath)
			if contentType != "" {
				c.Response().Header().Set("Content-Type", contentType)
			}
			return c.Attachment(filePath, filepath.Base(filePath))
		}

		return nil
	}
}

// TTSEndpoint handles CAMB AI async TTS (POST /apis/tts).
func TTSEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.CambAITTSRequest)
		if !ok {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("CAMB AI TTS request received", "model", input.Model)

		voice := fmt.Sprintf("%d", input.VoiceID)
		language := schema.CambAILanguageCodeFromID(input.LanguageID)

		filePath, _, err := backend.ModelTTS(input.Text, voice, language, ml, appConfig, *cfg)
		if err != nil {
			return err
		}

		taskID := uuid.New().String()
		ttsTaskResults.Store(taskID, filePath)

		return c.JSON(http.StatusOK, schema.CambAITaskResponse{
			TaskID: taskID,
			Status: "SUCCESS",
			RunID:  taskID,
		})
	}
}

// TTSTaskStatusEndpoint handles polling for async TTS results (GET /apis/tts/:task_id).
func TTSTaskStatusEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		taskID := c.Param("task_id")
		result, ok := ttsTaskResults.Load(taskID)
		if !ok {
			return c.JSON(http.StatusNotFound, schema.CambAIErrorResponse{
				Detail: "Task not found",
			})
		}

		filePath, ok := result.(string)
		if !ok {
			return c.JSON(http.StatusInternalServerError, schema.CambAIErrorResponse{
				Detail: "Invalid task result",
			})
		}

		filePath, contentType := audio.NormalizeAudioFile(filePath)
		if contentType != "" {
			c.Response().Header().Set("Content-Type", contentType)
		}
		return c.Attachment(filePath, filepath.Base(filePath))
	}
}
