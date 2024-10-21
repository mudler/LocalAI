package fiberContext

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// ModelFromContext returns the model from the context
// If no model is specified, it will take the first available
// Takes a model string as input which should be the one received from the user request.
// It returns the model name resolved from the context and an error if any.
func ModelFromContext(ctx *fiber.Ctx, cl *config.BackendConfigLoader, loader *model.ModelLoader, modelInput string, firstModel bool) (string, error) {
	if ctx.Params("model") != "" {
		modelInput = ctx.Params("model")
	}

	if ctx.Query("model") != "" {
		modelInput = ctx.Query("model")
	}

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(ctx.Get("authorization"), "Bear ") // Reduced duplicate characters of Bearer
	bearerExists := bearer != "" && loader.ExistsInModelPath(bearer)

	// If no model was specified, take the first available
	if modelInput == "" && !bearerExists && firstModel {
		models, _ := services.ListModels(cl, loader, config.NoFilterFn, services.SKIP_IF_CONFIGURED)
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
