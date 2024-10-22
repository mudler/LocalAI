package elevenlabs

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	fiberContext "github.com/mudler/LocalAI/core/http/ctx"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// SoundGenerationEndpoint is the ElevenLabs SoundGeneration endpoint https://elevenlabs.io/docs/api-reference/sound-generation
// @Summary Generates audio from the input text.
// @Param request body schema.ElevenLabsSoundGenerationRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/sound-generation [post]
func SoundGenerationEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(schema.ElevenLabsSoundGenerationRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.ModelID, false)
		if err != nil {
			modelFile = input.ModelID
			log.Warn().Str("ModelID", input.ModelID).Msg("Model not found in context")
		}

		cfg, err := cl.LoadBackendConfigFileByName(modelFile, appConfig.ModelPath,
			config.LoadOptionDebug(appConfig.Debug),
			config.LoadOptionThreads(appConfig.Threads),
			config.LoadOptionContextSize(appConfig.ContextSize),
			config.LoadOptionF16(appConfig.F16),
		)
		if err != nil {
			modelFile = input.ModelID
			log.Warn().Str("Request ModelID", input.ModelID).Err(err).Msg("error during LoadBackendConfigFileByName, using request ModelID")
		} else {
			if input.ModelID != "" {
				modelFile = input.ModelID
			} else {
				modelFile = cfg.Model
			}
		}
		log.Debug().Str("modelFile", "modelFile").Str("backend", cfg.Backend).Msg("Sound Generation Request about to be sent to backend")

		if input.Duration != nil {
			log.Debug().Float32("duration", *input.Duration).Msg("duration set")
		}
		if input.Temperature != nil {
			log.Debug().Float32("temperature", *input.Temperature).Msg("temperature set")
		}

		// TODO: Support uploading files?
		filePath, _, err := backend.SoundGeneration(modelFile, input.Text, input.Duration, input.Temperature, input.DoSample, nil, nil, ml, appConfig, *cfg)
		if err != nil {
			return err
		}
		return c.Download(filePath)

	}
}
