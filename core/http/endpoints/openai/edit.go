package openai

import (
	"encoding/json"
	"fmt"

	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/services"

	"github.com/gofiber/fiber/v2"

	"github.com/rs/zerolog/log"
)

func EditEndpoint(fce *fiberContext.FiberContextExtractor, oais *services.OpenAIService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		_, finalResultChannel, _, _, _, err := oais.Edit(request, false, request.Stream)
		if err != nil {
			return err
		}

		rawResponse := <-finalResultChannel
		if rawResponse.Error != nil {
			return rawResponse.Error
		}

		jsonResult, _ := json.Marshal(rawResponse.Value)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(rawResponse.Value)
	}
}
