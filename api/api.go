package api

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func App(opts ...AppOption) (*fiber.App, error) {
	options := newOptions(opts...)

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if options.debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
		BodyLimit:             options.uploadLimitMB * 1024 * 1024, // this is the default limit of 4MB
		DisableStartupMessage: options.disableMessage,
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

	if options.debug {
		app.Use(logger.New(logger.Config{
			Format: "[${ip}]:${port} ${status} - ${method} ${path}\n",
		}))
	}

	cm := NewConfigMerger()
	if err := cm.LoadConfigs(options.loader.ModelPath); err != nil {
		log.Error().Msgf("error loading config files: %s", err.Error())
	}

	if options.configFile != "" {
		if err := cm.LoadConfigFile(options.configFile); err != nil {
			log.Error().Msgf("error loading config file: %s", err.Error())
		}
	}

	if options.debug {
		for _, v := range cm.ListConfigs() {
			cfg, _ := cm.GetConfig(v)
			log.Debug().Msgf("Model: %s (config: %+v)", v, cfg)
		}
	}
	// Default middleware config
	app.Use(recover.New())

	if options.preloadJSONModels != "" {
		if err := ApplyGalleryFromString(options.loader.ModelPath, options.preloadJSONModels, cm); err != nil {
			return nil, err
		}
	}

	if options.preloadModelsFromPath != "" {
		if err := ApplyGalleryFromFile(options.loader.ModelPath, options.preloadModelsFromPath, cm); err != nil {
			return nil, err
		}
	}

	if options.cors {
		if options.corsAllowOrigins == "" {
			app.Use(cors.New())
		} else {
			app.Use(cors.New(cors.Config{
				AllowOrigins: options.corsAllowOrigins,
			}))
		}
	}

	// LocalAI API endpoints
	applier := newGalleryApplier(options.loader.ModelPath)
	applier.start(options.context, cm)
	app.Post("/models/apply", applyModelGallery(options.loader.ModelPath, cm, applier.C))
	app.Get("/models/jobs/:uuid", getOpStatus(applier))

	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", chatEndpoint(cm, options))
	app.Post("/chat/completions", chatEndpoint(cm, options))

	// edit
	app.Post("/v1/edits", editEndpoint(cm, options))
	app.Post("/edits", editEndpoint(cm, options))

	// completion
	app.Post("/v1/completions", completionEndpoint(cm, options))
	app.Post("/completions", completionEndpoint(cm, options))

	// embeddings
	app.Post("/v1/embeddings", embeddingsEndpoint(cm, options))
	app.Post("/embeddings", embeddingsEndpoint(cm, options))
	app.Post("/v1/engines/:model/embeddings", embeddingsEndpoint(cm, options))

	// audio
	app.Post("/v1/audio/transcriptions", transcriptEndpoint(cm, options))

	// images
	app.Post("/v1/images/generations", imageEndpoint(cm, options))

	if options.imageDir != "" {
		app.Static("/generated-images", options.imageDir)
	}

	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	// Kubernetes health checks
	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	// models
	app.Get("/v1/models", listModels(options.loader, cm))
	app.Get("/models", listModels(options.loader, cm))

	return app, nil
}
