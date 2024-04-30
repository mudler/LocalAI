package elevenlabs

import (
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/ctx"

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
func TTSEndpoint(ttsbs *backend.TextToSpeechBackendService, fce *ctx.FiberContentExtractor) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.ElevenLabsTTSRequest)
		voiceID := c.Params("voice-id")

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fce.ModelFromContext(c, input.ModelID, false)
		if err != nil {
			modelFile = input.ModelID
			log.Warn().Msgf("Model not found in context: %s", input.ModelID)
		}

		log.Debug().Str("modelName", modelFile).Msg("elevenlabs TTS request recieved for model")

		ttsRequest := &schema.TTSRequest{
			Model: input.ModelID,
			Input: input.Text,
			Voice: voiceID,
		}

		jr := ttsbs.TextToAudioFile(ttsRequest)
		filePathPtr, err := jr.Wait()
		if err != nil {
			return err
		}
		if filePathPtr == nil {
			err := fmt.Errorf("recieved a nil filepath from TextToAudioFile")
			log.Error().Err(err).Msg("eleventlabs TTSEndpoint error")
			return err
		}
		return c.Download(*filePathPtr)
	}
}
