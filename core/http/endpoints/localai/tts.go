package localai

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
// @Param request body schema.TTSRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/audio/speech [post]
func TTSEndpoint(ttsbs *backend.TextToSpeechBackendService, fce *ctx.FiberContentExtractor) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.TTSRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			log.Error().Err(err).Msg("Error during BodyParser")
			return err
		}

		modelFile, err := fce.ModelFromContext(c, input.Model, "", false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Str("input.Model", input.Model).Msg("Model not found in context, using input.Model")
		} else {
			log.Debug().Str("initial input.Model", input.Model).Str("modelFile", modelFile).Msg("overwriting input.Model with modelFile")
			input.Model = modelFile
		}

		log.Debug().Str("modelName", modelFile).Msg("localai TTS request recieved for model")

		jr := ttsbs.TextToAudioFile(input)
		log.Debug().Msg("Obtained JobResult, waiting")
		filePathPtr, err := jr.Wait()
		if err != nil {
			log.Error().Err(err).Msg("Error during TextToAudioFile")
			return err
		}
		if filePathPtr == nil {
			err := fmt.Errorf("recieved a nil filepath from TextToAudioFile")
			log.Error().Err(err).Msg("localai TTSEndpoint error")
			return err
		}
		log.Debug().Str("filePath", *filePathPtr).Msg("Successfully created output audio file at filePath")
		return c.Download(*filePathPtr)
	}
}
