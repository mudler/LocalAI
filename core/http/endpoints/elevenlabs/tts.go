package elevenlabs

import (
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// TTSEndpoint is the OpenAI Speech API endpoint https://platform.openai.com/docs/api-reference/audio/createSpeech
// @Summary Generates audio from the input text.
// @Param  voice-id	path string	true	"Account ID"
// @Param request body schema.TTSRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/text-to-speech/{voice-id} [post]
func TTSEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {

		voiceID := c.Param("voice-id")

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.ElevenLabsTTSRequest)
		if !ok || input.ModelID == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		log.Debug().Str("modelName", input.ModelID).Msg("elevenlabs TTS request received")

		filePath, _, err := backend.ModelTTS(input.Text, voiceID, input.LanguageCode, ml, appConfig, *cfg)
		if err != nil {
			return err
		}
		return c.Attachment(filePath, filepath.Base(filePath))
	}
}
