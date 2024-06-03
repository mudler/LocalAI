package localai

import (
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/middleware"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// TTSEndpoint is the OpenAI Speech API endpoint https://platform.openai.com/docs/api-reference/audio/createSpeech
//
//		@Summary	Generates audio from the input text.
//	 @Accept json
//	 @Produce audio/x-wav
//		@Param		request	body		schema.TTSRequest	true	"query params"
//		@Success	200		{string}	binary				"generated audio/wav file"
//		@Router		/v1/audio/speech [post]
//		@Router		/tts [post]
func TTSEndpoint(ttsbs *backend.TextToSpeechBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.TTSRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			log.Error().Err(err).Msg("Error during BodyParser")
			return err
		}

		localModelName, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
		if ok && localModelName != "" {
			input.Model = localModelName
		}

		if input.Model == "" {
			return fmt.Errorf("model is required, no default available")
		}

		log.Debug().Str("modelName", input.Model).Msg("localai TTS request recieved for model")

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
