package ctx

// This needs to be in a distinct package to avoid cycles between http, routes, and endpoints!

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// This type largely exists to drop permissions from ModelLoader -
// various endpoint functions do not need access to the real "ModelLoader" api, and this type makes that clear.
type FiberContentExtractor struct {
	ml        *model.ModelLoader
	appConfig *config.ApplicationConfig
}

func NewFiberContentExtractor(ml *model.ModelLoader, appConfig *config.ApplicationConfig) *FiberContentExtractor {
	return &FiberContentExtractor{
		ml:        ml,
		appConfig: appConfig,
	}
}

// ModelFromContext returns the model from the context
// If no model is specified, it will take the first available
// Takes a model string as input which should be the one received from the user request.
// It returns the model name resolved from the context and an error if any.
func (fce *FiberContentExtractor) ModelFromContext(ctx *fiber.Ctx, modelInput string, firstModel bool) (string, error) {
	if ctx.Params("model") != "" {
		modelInput = ctx.Params("model")
	}

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(ctx.Get("authorization"), "Bearer ")
	bearerExists := bearer != "" && fce.ml.ExistsInModelPath(bearer)

	// If no model was specified, take the first available
	if modelInput == "" && !bearerExists && firstModel {
		models, _ := fce.ml.ListModels()
		if len(models) > 0 {
			modelInput = models[0]
			log.Debug().Msgf("No model specified, using: %s", modelInput)
		} else {
			log.Debug().Msgf("No model specified, returning error")
			return "", fmt.Errorf("no model specified")
		}
	}

	// If a model is found in bearer token takes precedence
	if bearerExists {
		log.Debug().Msgf("Using model from bearer token: %s", bearer)
		modelInput = bearer
	}
	return modelInput, nil
}

func (fce *FiberContentExtractor) OpenAIRequestFromContext(ctx *fiber.Ctx, firstModel bool) (*schema.OpenAIRequest, error) {
	input := new(schema.OpenAIRequest)

	// Get input data from the request body
	if err := ctx.BodyParser(input); err != nil {
		return nil, fmt.Errorf("failed parsing request body: %w", err)
	}

	context, cancel := context.WithCancel(fce.appConfig.Context)
	input.Context = context
	input.Cancel = cancel

	modelName, err := fce.ModelFromContext(ctx, input.Model, firstModel)
	input.Model = modelName

	return input, err
}
