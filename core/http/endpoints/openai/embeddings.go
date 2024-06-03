package openai

import (
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/middleware"
	"github.com/go-skynet/LocalAI/core/schema"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// EmbeddingsEndpoint is the OpenAI Embeddings API endpoint https://platform.openai.com/docs/api-reference/embeddings
// @Summary Get a vector representation of a given input that can be easily consumed by machine learning models and algorithms.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/embeddings [post]
func EmbeddingsEndpoint(ebs *backend.EmbeddingsBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		request, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_OPENAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || request == nil {
			return fiber.ErrBadRequest
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
