package localai

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/pkg/utils"
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
func TTSEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.TTSRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}

		cfg, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return fiber.ErrBadRequest
		}

		log.Debug().Str("model", input.Model).Msg("LocalAI TTS Request received")

		if cfg.Backend == "" && input.Backend != "" {
			cfg.Backend = input.Backend
		}

		if input.Language != "" {
			cfg.Language = input.Language
		}

		if input.Voice != "" {
			cfg.Voice = input.Voice
		}

		filePath, _, err := backend.ModelTTS(input.Input, cfg.Voice, cfg.Language, ml, appConfig, *cfg)
		if err != nil {
			return err
		}

		// Convert generated file to target format
		filePath, err = utils.AudioConvert(filePath, input.Format)
		if err != nil {
			return err
		}

		return c.Download(filePath)
	}
}
