package openai

import (
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/ctx"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// EmbeddingsEndpoint is the OpenAI Embeddings API endpoint https://platform.openai.com/docs/api-reference/embeddings
// @Summary Get a vector representation of a given input that can be easily consumed by machine learning models and algorithms.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/embeddings [post]
func EmbeddingsEndpoint(ebs *backend.EmbeddingsBackendService, fce *ctx.FiberContentExtractor) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		request, err := fce.OpenAIRequestFromContext(c, "", true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request: %w", err)
		}

		jr := ebs.Embeddings(request)
		resp, err := jr.Wait()

		if err != nil {
			log.Error().Err(err).Msg("error during embedding")
			return err
		}
		if resp == nil {
			err := fmt.Errorf("recieved a nil response from embeddings backend")
			log.Error().Err(err).Msg("EmbeddingsEndpoint nil result")
			return err
		}

		// Return the prediction in the response body
		return c.JSON(*resp)
	}
}
