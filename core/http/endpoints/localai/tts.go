package localai

import (
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// TTSEndpoint is the OpenAI Speech API endpoint https://platform.openai.com/docs/api-reference/audio/createSpeech
//
//		@Summary	Generates audio from the input text.
//	 	@Accept json
//	 	@Produce audio/x-wav
//		@Param		request	body		schema.TTSRequest	true	"query params"
//		@Success	200		{string}	binary				"generated audio/wav file"
//		@Router		/v1/audio/speech [post]
//		@Router		/tts [post]
func TTSEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.TTSRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("LocalAI TTS Request received", "model", input.Model)

		if cfg.Backend == "" && input.Backend != "" {
			cfg.Backend = input.Backend
		}

		if input.Language != "" {
			cfg.Language = input.Language
		}

		if input.Voice != "" {
			cfg.Voice = input.Voice
		}

		// Handle streaming TTS
		if input.Stream {
			// Set headers for streaming audio
			c.Response().Header().Set("Content-Type", "audio/wav")
			c.Response().Header().Set("Transfer-Encoding", "chunked")
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("Connection", "keep-alive")

			// Stream audio chunks as they're generated
			err := backend.ModelTTSStream(input.Input, cfg.Voice, cfg.Language, ml, appConfig, *cfg, func(audioChunk []byte) error {
				_, writeErr := c.Response().Write(audioChunk)
				if writeErr != nil {
					return writeErr
				}
				c.Response().Flush()
				return nil
			})
			if err != nil {
				return err
			}

			return nil
		}

		// Non-streaming TTS (existing behavior)
		filePath, _, err := backend.ModelTTS(input.Input, cfg.Voice, cfg.Language, ml, appConfig, *cfg)
		if err != nil {
			return err
		}

		// Resample to requested sample rate if specified
		if input.SampleRate > 0 {
			filePath, err = utils.AudioResample(filePath, input.SampleRate)
			if err != nil {
				return err
			}
		}

		// Convert generated file to target format
		filePath, err = utils.AudioConvert(filePath, input.Format)
		if err != nil {
			return err
		}

		filePath, contentType := audio.NormalizeAudioFile(filePath)
		if contentType != "" {
			c.Response().Header().Set("Content-Type", contentType)
		}
		return c.Attachment(filePath, filepath.Base(filePath))
	}
}
