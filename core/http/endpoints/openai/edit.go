package openai

import (
	"encoding/json"
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"

	"github.com/gofiber/fiber/v2"

	"github.com/rs/zerolog/log"
)

func EditEndpoint(fce *fiberContext.FiberContextExtractor, llmbs *backend.LLMBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		resp, err := llmbs.Edit(request)
		if err != nil {
			return err
		}

		jsonResult, err := json.Marshal(resp)
		if err != nil {
			return err
		}

		log.Debug().Msgf("Response: %s", jsonResult)
		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
