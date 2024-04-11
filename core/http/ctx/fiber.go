package fiberContext

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

type FiberContextExtractor struct {
	ml        *model.ModelLoader
	appConfig *config.ApplicationConfig
}

func NewFiberContextExtractor(ml *model.ModelLoader, appConfig *config.ApplicationConfig) *FiberContextExtractor {
	return &FiberContextExtractor{
		ml:        ml,
		appConfig: appConfig,
	}
}

// ModelFromContext returns the model from the context
// If no model is specified, it will take the first available
// Takes a model string as input which should be the one received from the user request.
// It returns the model name resolved from the context and an error if any.
func (fce *FiberContextExtractor) ModelFromContext(ctx *fiber.Ctx, modelInput string, firstModel bool) (string, error) {
	ctxPM := ctx.Params("model")
	if ctxPM != "" {
		log.Debug().Msgf("[FCE] Overriding param modelInput %q with ctx.Params value %q", modelInput, ctxPM)
		modelInput = ctxPM
	}

	// Set model from bearer token, if available
	bearer := strings.TrimPrefix(ctx.Get("authorization"), "Bearer ")
	bearerExists := bearer != "" && fce.ml.ExistsInModelPath(bearer)

	// If no model was specified, take the first available
	if modelInput == "" && !bearerExists && firstModel {
		models, _ := fce.ml.ListModels()
		if len(models) > 0 {
			modelInput = models[0]
			log.Debug().Msgf("[FCE] No model specified, using first available: %s", modelInput)
		} else {
			log.Warn().Msgf("[FCE] No model specified, none available")
			return "", fmt.Errorf("[fce] no model specified, none available")
		}
	}

	// If a model is found in bearer token takes precedence
	if bearerExists {
		log.Debug().Msgf("[FCE] Using model from bearer token: %s", bearer)
		modelInput = bearer
	}

	if modelInput == "" {
		log.Warn().Msg("[FCE] modelInput is empty")
	}
	return modelInput, nil
}

// TODO: Do we still need the first return value?
func (fce *FiberContextExtractor) OpenAIRequestFromContext(c *fiber.Ctx, firstModel bool) (string, *schema.OpenAIRequest, error) {
	input := new(schema.OpenAIRequest)

	// Get input data from the request body
	if err := c.BodyParser(input); err != nil {
		return "", nil, fmt.Errorf("failed parsing request body: %w", err)
	}

	received, _ := json.Marshal(input)

	ctx, cancel := context.WithCancel(fce.appConfig.Context)
	input.Context = ctx
	input.Cancel = cancel

	log.Debug().Msgf("Request received: %s", string(received))

	var err error
	input.Model, err = fce.ModelFromContext(c, input.Model, firstModel)

	return input.Model, input, err
}
