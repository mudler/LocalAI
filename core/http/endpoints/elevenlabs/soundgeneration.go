package elevenlabs

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// SoundGenerationEndpoint is the ElevenLabs SoundGeneration endpoint https://elevenlabs.io/docs/api-reference/sound-generation
// @Summary Generates audio from the input text.
// @Param request body schema.ElevenLabsSoundGenerationRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/sound-generation [post]
func SoundGenerationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.ElevenLabsSoundGenerationRequest)
		if !ok || input.ModelID == "" {
			return fiber.ErrBadRequest
		}

		cfg, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return fiber.ErrBadRequest
		}

		log.Debug().Str("modelFile", "modelFile").Str("backend", cfg.Backend).Msg("Sound Generation Request about to be sent to backend")

		// TODO: Support uploading files?
		filePath, _, err := backend.SoundGeneration(input.Text, input.Duration, input.Temperature, input.DoSample, nil, nil, ml, appConfig, *cfg)
		if err != nil {
			return err
		}
		return c.Download(filePath)

	}
}
