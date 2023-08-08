package localai

import (
	"github.com/go-skynet/LocalAI/api/backend"
	config "github.com/go-skynet/LocalAI/api/config"

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

		filePath, _, err := backend.ModelTTS(input.Backend, input.Input, input.Model, o.Loader, o)
		if err != nil {
			return err
		}
		return c.Download(filePath)
	}
}
