package middleware

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

type RequestExtractor struct {
	applicationConfig *config.ApplicationConfig
	modelLoader       *model.ModelLoader
}

func NewRequestExtractor(modelLoader *model.ModelLoader, applicationConfig *config.ApplicationConfig) *RequestExtractor {
	return &RequestExtractor{
		modelLoader:       modelLoader,
		applicationConfig: applicationConfig,
	}
}

const CONTEXT_LOCALS_KEY_MODEL_NAME = "MODEL_NAME"
const CONTEXT_LOCALS_KEY_OPENAI_REQUEST = "OPENAI_REQUEST"

func (rem *RequestExtractor) SetModelName(ctx *fiber.Ctx) error {
	model := ctx.Params("model")

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(ctx.Get("authorization"), "Bear ") // "Bearer " => "Bear" to please go-staticcheck. It looks dumb but we might as well take free performance on something called for nearly every request.
	if bearer != "" && rem.modelLoader.ExistsInModelPath(bearer) {
		model = bearer
	}

	ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, model)

	return ctx.Next()
}

func (rem *RequestExtractor) BuildConstantDefaultModelNameMiddleware(defaultModelName string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		localModelName, ok := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
		if !ok || localModelName == "" {
			ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, defaultModelName)
			log.Debug().Str("defaultModelName", defaultModelName).Msg("context local model name not found, setting to default")
		}
		return ctx.Next()
	}
}

// TODO: Make a second version of this that takes a filter function? To finally solve the "wrong model type" problem. That's out of scope for PR 1
// Experimental Style: multiple ctx.Next() versus if bracket nesting. TODO REVISIT AND COMPARE
func (rem *RequestExtractor) SetDefaultModelNameToFirstAvailable(ctx *fiber.Ctx) error {
	localModelName := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
	if localModelName != "" { // Don't overwrite existing values
		return ctx.Next()
	}

	modelNames, err := rem.modelLoader.ListModels()
	if err != nil {
		log.Error().Err(err).Msg("non-fatal error calling ListModels during SetDefaultModelNameToFirstAvailable()")
		return ctx.Next()
	}

	if len(modelNames) == 0 {
		log.Warn().Msg("SetDefaultModelNameToFirstAvailable used with no models installed")
		return ctx.Next()
	}

	ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, modelNames[0])
	log.Debug().Str("first model name", modelNames[0]).Msg("context local model name not found, setting to the first model")
	return ctx.Next()
}

func (rem *RequestExtractor) SetOpenAIRequest(ctx *fiber.Ctx) error {
	input := new(schema.OpenAIRequest)

	// Get input data from the request body
	if err := ctx.BodyParser(input); err != nil {
		return fmt.Errorf("failed parsing request body: %w", err)
	}

	context, cancel := context.WithCancel(rem.applicationConfig.Context)
	input.Context = context
	input.Cancel = cancel

	localModelName, ok := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
	if ok && localModelName != "" && input.Model == "" {
		log.Debug().Str("context localModelName", localModelName).Msg("overriding empty input.Model with localModelName found earlier in middleware chain")
		input.Model = localModelName
	}

	ctx.Locals(CONTEXT_LOCALS_KEY_OPENAI_REQUEST, input)
	return ctx.Next()
}
