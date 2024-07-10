package elevenlabs

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	fiberContext "github.com/mudler/LocalAI/core/http/ctx"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/rs/zerolog/log"
)

// TTSEndpoint is the OpenAI Speech API endpoint https://platform.openai.com/docs/api-reference/audio/createSpeech
// @Summary Generates audio from the input text.
// @Param  voice-id	path string	true	"Account ID"
// @Param request body schema.TTSRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/text-to-speech/{voice-id} [post]
func TTSEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.ElevenLabsTTSRequest)
		voiceID := c.Params("voice-id")

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.ModelID, false)
		if err != nil {
			modelFile = input.ModelID
			log.Warn().Msgf("Model not found in context: %s", input.ModelID)
		}

		cfg, err := cl.LoadBackendConfigFileByName(modelFile, appConfig.ModelPath,
			config.LoadOptionDebug(appConfig.Debug),
			config.LoadOptionThreads(appConfig.Threads),
			config.LoadOptionContextSize(appConfig.ContextSize),
			config.LoadOptionF16(appConfig.F16),
		)
		if err != nil {
			modelFile = input.ModelID
			log.Warn().Msgf("Model not found in context: %s", input.ModelID)
		} else {
			if input.ModelID != "" {
				modelFile = input.ModelID
			} else {
				modelFile = cfg.Model
			}
		}
		log.Debug().Msgf("Request for model: %s", modelFile)

		filePath, _, err := backend.ModelTTS(cfg.Backend, input.Text, modelFile, "", voiceID, ml, appConfig, *cfg)
		if err != nil {
			return err
		}
		return c.Download(filePath)
	}
}
