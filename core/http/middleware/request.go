package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

type RequestExtractor struct {
	applicationConfig *config.ApplicationConfig
	listModelService  *services.ListModels
}

func NewRequestExtractor(listModelService *services.ListModels, applicationConfig *config.ApplicationConfig) *RequestExtractor {
	return &RequestExtractor{
		applicationConfig: applicationConfig,
		listModelService:  listModelService,
	}
}

const CONTEXT_LOCALS_KEY_MODEL_NAME = "MODEL_NAME"
const CONTEXT_LOCALS_KEY_OPENAI_REQUEST = "OPENAI_REQUEST"

func (rem *RequestExtractor) SetModelName(ctx *fiber.Ctx) error {
	model := ctx.Params("model")

	// Set model from bearer token, if available
	bearer := strings.TrimLeft(ctx.Get("authorization"), "Bear ") // "Bearer " => "Bear" to please go-staticcheck. It looks dumb but we might as well take free performance on something called for nearly every request.
	exists, err := rem.listModelService.CheckExistence(bearer, services.ALWAYS_INCLUDE)
	if err == nil && exists && bearer != "" {
		model = bearer
	}

	ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, model)

	return ctx.Next()
}

func (re *RequestExtractor) BuildConstantDefaultModelNameMiddleware(defaultModelName string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		localModelName, ok := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
		if !ok || localModelName == "" {
			ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, defaultModelName)
			log.Debug().Str("defaultModelName", defaultModelName).Msg("context local model name not found, setting to default")
		}
		return ctx.Next()
	}
}

func (re *RequestExtractor) BuildFilteredFirstAvailableDefaultModel(filterFn config.BackendConfigFilterFn) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		localModelName := ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
		if localModelName != "" { // Don't overwrite existing values
			return ctx.Next()
		}

		modelNames, err := re.listModelService.ListModels(filterFn, services.SKIP_IF_CONFIGURED)
		log.Debug().Int("len(modelNames)", len(modelNames)).Msg("BuildFilteredFirstAvailableDefaultModel ListModels")
		if err != nil {
			log.Error().Err(err).Msg("non-fatal error calling ListModels during SetDefaultModelNameToFirstAvailable()")
			return ctx.Next()
		}

		if len(modelNames) == 0 {
			log.Warn().Msg("SetDefaultModelNameToFirstAvailable used with no matching models installed")
			// return errors.New("this endpoint requires at least one model to be installed")
			// This is non-fatal - making it so was breaking the case of direct installation of raw models
			return ctx.Next()
		}

		ctx.Locals(CONTEXT_LOCALS_KEY_MODEL_NAME, modelNames[0].ID)
		log.Debug().Str("first model name", modelNames[0].ID).Msg("context local model name not found, setting to the first model")
		return ctx.Next()
	}
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
