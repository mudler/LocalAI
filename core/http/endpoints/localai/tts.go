package localai

import (
	"github.com/go-skynet/LocalAI/core/backend"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// TTSEndpoint is the OpenAI Speech API endpoint https://platform.openai.com/docs/api-reference/audio/createSpeech
// @Summary Generates audio from the input text.
// @Param request body schema.TTSRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/audio/speech [post]
func TTSEndpoint(fce *fiberContext.FiberContextExtractor, ttsbs *backend.TextToSpeechBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		var err error
		input := new(schema.TTSRequest)

		// Get input data from the request body
		if err = c.BodyParser(input); err != nil {
			return err
		}

		input.Model, err = fce.ModelFromContext(c, input.Model, false)
		if err != nil {
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}

		responseChannel := ttsbs.TextToAudioFile(input)
		rawValue := <-responseChannel
		if rawValue.Error != nil {
			return rawValue.Error
		}
		return c.Download(*rawValue.Value)
	}
}
