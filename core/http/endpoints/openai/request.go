package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

func readInput(c *fiber.Ctx, o *datamodel.StartupOptions, ml *model.ModelLoader, randomModel bool) (string, *datamodel.OpenAIRequest, error) {
	input := new(datamodel.OpenAIRequest)
	ctx, cancel := context.WithCancel(o.Context)
	input.Context = ctx
	input.Cancel = cancel
	// Get input data from the request body
	if err := c.BodyParser(input); err != nil {
		return "", nil, fmt.Errorf("failed parsing request body: %w", err)
	}

	modelFile := input.Model

	if c.Params("model") != "" {
		modelFile = c.Params("model")
	}

	received, _ := json.Marshal(input)

	log.Debug().Msgf("Request received: %s", string(received))

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(c.Get("authorization"), "Bearer ")
	bearerExists := bearer != "" && ml.ExistsInModelPath(bearer)

	// If no model was specified, take the first available
	if modelFile == "" && !bearerExists && randomModel {
		models, _ := ml.ListModels()
		if len(models) > 0 {
			modelFile = models[0]
			log.Debug().Msgf("No model specified, using: %s", modelFile)
		} else {
			log.Debug().Msgf("No model specified, returning error")
			return "", nil, fmt.Errorf("no model specified")
		}
	}

	// If a model is found in bearer token takes precedence
	if bearerExists {
		log.Debug().Msgf("Using model from bearer token: %s", bearer)
		modelFile = bearer
	}
	return modelFile, input, nil
}
