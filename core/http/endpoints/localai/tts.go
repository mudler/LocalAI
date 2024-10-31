package localai

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	fiberContext "github.com/mudler/LocalAI/core/http/ctx"
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
func TTSEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.TTSRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.Model, false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}

		cfg, err := cl.LoadBackendConfigFileByName(modelFile, appConfig.ModelPath,
			config.LoadOptionDebug(appConfig.Debug),
			config.LoadOptionThreads(appConfig.Threads),
			config.LoadOptionContextSize(appConfig.ContextSize),
			config.LoadOptionF16(appConfig.F16),
		)

		if err != nil {
			log.Err(err)
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		} else {
			modelFile = cfg.Model
		}
		log.Debug().Msgf("Request for model: %s", modelFile)

		if input.Backend != "" {
			cfg.Backend = input.Backend
		}

		if input.Language != "" {
			cfg.Language = input.Language
		}

		if input.Voice != "" {
			cfg.Voice = input.Voice
		}

		filePath, _, err := backend.ModelTTS(cfg.Backend, input.Input, modelFile, cfg.Voice, cfg.Language, ml, appConfig, *cfg)
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
