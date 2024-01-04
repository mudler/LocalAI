package openai

import (
	"encoding/json"
	"fmt"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/schema"
	"github.com/gofiber/fiber/v2"

	"github.com/rs/zerolog/log"
)

func EditEndpoint(cl *services.ConfigLoader, ml *model.ModelLoader, so *schema.StartupOptions) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		modelFile, input, err := readInput(c, so, ml, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		resp, err := backend.EditGenerationOpenAIRequest(modelFile, input, cl, ml, so)
		if err != nil {
			return err
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
