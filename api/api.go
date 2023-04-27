package api

import (
	"errors"

	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func App(configFile string, loader *model.ModelLoader, threads, ctxSize int, f16 bool, debug, disableMessage bool) *fiber.App {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
		DisableStartupMessage: disableMessage,
		// Override default error handler
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			// Status code defaults to 500
			code := fiber.StatusInternalServerError

			// Retrieve the custom status code if it's a *fiber.Error
			var e *fiber.Error
			if errors.As(err, &e) {
				code = e.Code
			}

			// Send custom error page
			return ctx.Status(code).JSON(
				ErrorResponse{
					Error: &APIError{Message: err.Error(), Code: code},
				},
			)
		},
	})

	cm := make(ConfigMerger)
	if err := cm.LoadConfigs(loader.ModelPath); err != nil {
		log.Error().Msgf("error loading config files: %s", err.Error())
	}

	if configFile != "" {
		if err := cm.LoadConfigFile(configFile); err != nil {
			log.Error().Msgf("error loading config file: %s", err.Error())
		}
	}

	if debug {
		for k, v := range cm {
			log.Debug().Msgf("Model: %s (config: %+v)", k, v)
		}
	}
	// Default middleware config
	app.Use(recover.New())
	app.Use(cors.New())

	// openAI compatible API endpoint
	app.Post("/v1/chat/completions", openAIEndpoint(cm, true, debug, loader, threads, ctxSize, f16))
	app.Post("/chat/completions", openAIEndpoint(cm, true, debug, loader, threads, ctxSize, f16))

	app.Post("/v1/completions", openAIEndpoint(cm, false, debug, loader, threads, ctxSize, f16))
	app.Post("/completions", openAIEndpoint(cm, false, debug, loader, threads, ctxSize, f16))

	app.Get("/v1/models", listModels(loader, cm))
	app.Get("/models", listModels(loader, cm))

	return app
}
