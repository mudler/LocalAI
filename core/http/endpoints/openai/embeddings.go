package openai

import (
	"encoding/json"
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// https://platform.openai.com/docs/api-reference/embeddings
func EmbeddingsEndpoint(fce *fiberContext.FiberContextExtractor, ebs *backend.EmbeddingsBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		_, input, err := fce.OpenAIRequestFromContext(c, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		responseChannel := ebs.Embeddings(input)

		rawResponse := <-responseChannel

		if rawResponse.Error != nil {
			return rawResponse.Error
		}

		jsonResult, _ := json.Marshal(rawResponse.Value)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(rawResponse.Value)
	}
}
