package elevenlabs

import (
	"github.com/go-skynet/LocalAI/core/backend"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// TTSEndpoint is the OpenAI Speech API endpoint https://platform.openai.com/docs/api-reference/audio/createSpeech
// @Summary Generates audio from the input text.
// @Param  voice-id	path string	true	"Account ID"
// @Param request body schema.TTSRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/text-to-speech/{voice-id} [post]
func TTSEndpoint(fce *fiberContext.FiberContextExtractor, ttsbs *backend.TextToSpeechBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.ElevenLabsTTSRequest)
		voiceID := c.Params("voice-id")

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		var err error
		input.ModelID, err = fce.ModelFromContext(c, input.ModelID, false)
		if err != nil {
			log.Warn().Msgf("Model not found in context: %s", input.ModelID)
		}

		responseChannel := ttsbs.TextToAudioFile(&schema.TTSRequest{
			Model: input.ModelID,
			Voice: voiceID,
			Input: input.Text,
		})
		rawValue := <-responseChannel
		if rawValue.Error != nil {
			return rawValue.Error
		}
		return c.Download(*rawValue.Value)
	}
}
