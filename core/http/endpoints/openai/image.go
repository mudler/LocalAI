package openai

import (
	"encoding/json"
	"fmt"

	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"

	"github.com/go-skynet/LocalAI/core/backend"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// https://platform.openai.com/docs/api-reference/images/create

/*
*

	curl http://localhost:8080/v1/images/generations \
	  -H "Content-Type: application/json" \
	  -d '{
	    "prompt": "A cute baby sea otter",
	    "n": 1,
	    "size": "512x512"
	  }'

*
*/

// ImageEndpoint is the OpenAI Image generation API endpoint https://platform.openai.com/docs/api-reference/images/create
// @Summary Creates an image given a prompt.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/images/generations [post]
func ImageEndpoint(fce *fiberContext.FiberContextExtractor, igbs *backend.ImageGenerationBackendService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// TODO: Somewhat a hack. Is there a better place to assign this?
		if igbs.BaseUrlForGeneratedImages == "" {
			igbs.BaseUrlForGeneratedImages = c.BaseURL() + "/generated-images/"
		}
		_, request, err := fce.OpenAIRequestFromContext(c, false)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		responseChannel := igbs.GenerateImage(request)
		rawResponse := <-responseChannel

		if rawResponse.Error != nil {
			return rawResponse.Error
		}

		jsonResult, err := json.Marshal(rawResponse.Value)
		if err != nil {
			return err
		}
		log.Debug().Msgf("Response: %s", jsonResult)
		// Return the prediction in the response body
		return c.JSON(rawResponse.Value)
	}
}
