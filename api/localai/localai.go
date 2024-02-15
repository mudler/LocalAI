package localai

import (
	"github.com/go-skynet/LocalAI/api/backend"
	config "github.com/go-skynet/LocalAI/api/config"
	fiberContext "github.com/go-skynet/LocalAI/api/ctx"
	"github.com/rs/zerolog/log"

	"github.com/go-skynet/LocalAI/api/options"
	"github.com/gofiber/fiber/v2"
)

type TTSRequest struct {
	Model   string `json:"model" yaml:"model"`
	Input   string `json:"input" yaml:"input"`
	Backend string `json:"backend" yaml:"backend"`
}

func TTSEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(TTSRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fiberContext.ModelFromContext(c, o.Loader, input.Model, false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}
		cfg, err := config.Load(modelFile, o.Loader.ModelPath, cm, false, 0, 0, false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		} else {
			modelFile = cfg.Model
		}
		log.Debug().Msgf("Request for model: %s", modelFile)

		if input.Backend != "" {
			cfg.Backend = input.Backend
		}

		filePath, _, err := backend.ModelTTS(cfg.Backend, input.Input, modelFile, o.Loader, o, *cfg)
		if err != nil {
			return err
		}
		return c.Download(filePath)
	}
}
