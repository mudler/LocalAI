package api

import (
	"context"
	"errors"

	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func App(c context.Context, configFile string, loader *model.ModelLoader, uploadLimitMB, threads, ctxSize int, f16 bool, debug, disableMessage bool, imageDir string) *fiber.App {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
		BodyLimit:             uploadLimitMB * 1024 * 1024, // this is the default limit of 4MB
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

	if debug {
		app.Use(logger.New(logger.Config{
			Format: "[${ip}]:${port} ${status} - ${method} ${path}\n",
		}))
	}

	cm := NewConfigMerger()
	if err := cm.LoadConfigs(loader.ModelPath); err != nil {
		log.Error().Msgf("error loading config files: %s", err.Error())
	}

	if configFile != "" {
		if err := cm.LoadConfigFile(configFile); err != nil {
			log.Error().Msgf("error loading config file: %s", err.Error())
		}
	}

	if debug {
		for _, v := range cm.ListConfigs() {
			cfg, _ := cm.GetConfig(v)
			log.Debug().Msgf("Model: %s (config: %+v)", v, cfg)
		}
	}
	// Default middleware config
	app.Use(recover.New())
	app.Use(cors.New())

	// LocalAI API endpoints
	applier := newGalleryApplier(loader.ModelPath)
	applier.start(c, cm)
	app.Post("/models/apply", applyModelGallery(loader.ModelPath, cm, applier.C))
	app.Get("/models/jobs/:uuid", getOpStatus(applier))

	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", chatEndpoint(cm, debug, loader, threads, ctxSize, f16))
	app.Post("/chat/completions", chatEndpoint(cm, debug, loader, threads, ctxSize, f16))

	// edit
	app.Post("/v1/edits", editEndpoint(cm, debug, loader, threads, ctxSize, f16))
	app.Post("/edits", editEndpoint(cm, debug, loader, threads, ctxSize, f16))

	// completion
	app.Post("/v1/completions", completionEndpoint(cm, debug, loader, threads, ctxSize, f16))
	app.Post("/completions", completionEndpoint(cm, debug, loader, threads, ctxSize, f16))

	// embeddings
	app.Post("/v1/embeddings", embeddingsEndpoint(cm, debug, loader, threads, ctxSize, f16))
	app.Post("/embeddings", embeddingsEndpoint(cm, debug, loader, threads, ctxSize, f16))
	app.Post("/v1/engines/:model/embeddings", embeddingsEndpoint(cm, debug, loader, threads, ctxSize, f16))

	// audio
	app.Post("/v1/audio/transcriptions", transcriptEndpoint(cm, debug, loader, threads, ctxSize, f16))

	// images
	app.Post("/v1/images/generations", imageEndpoint(cm, debug, loader, imageDir))

	if imageDir != "" {
		app.Static("/generated-images", imageDir)
	}

	// models
	app.Get("/v1/models", listModels(loader, cm))
	app.Get("/models", listModels(loader, cm))

	return app
}
