package jina

import (
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/schema"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

func JINARerankEndpoint(rbs *backend.RerankBackendService, fce *ctx.FiberContentExtractor) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		req := new(schema.JINARerankRequest)
		if err := c.BodyParser(req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Cannot parse JSON",
			})
		}

		input := new(schema.RerankRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fce.ModelFromContext(c, input.Model, false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}

		log.Debug().Msgf("jina rerank request for model: %s", modelFile)

		jr := rbs.Rerank(input)

		response, err := jr.Wait()
		if err != nil {
			log.Error().Err(err).Msg("error during jina rerank")
			return err
		}
		if response == nil {
			err := fmt.Errorf("recieved a nil response from Rerank backend")
			log.Error().Err(err).Msg("jina rerank nil result")
			return err
		}

		return c.Status(fiber.StatusOK).JSON(*response)
	}
}
