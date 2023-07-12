package api

import (
	"errors"
	"strings"

	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/assets"
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

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", options.threads, options.loader.ModelPath)
	log.Info().Msgf("LocalAI version: %s", internal.PrintableVersion())

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

	if options.assetsDestination != "" {
		// Extract files from the embedded FS
		err := assets.ExtractFiles(options.backendAssets, options.assetsDestination)
		if err != nil {
			log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly, like gpt4all)", err)
		}
	}

	// Default middleware config
	app.Use(recover.New())

	// Auth middleware checking if API key is valid. If no API key is set, no auth is required.
	auth := func(c *fiber.Ctx) error {
		if options.apiKey != "" {
			authHeader := c.Get("Authorization")
			if authHeader == "" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Authorization header missing"})
			}
			authHeaderParts := strings.Split(authHeader, " ")
			if len(authHeaderParts) != 2 || authHeaderParts[0] != "Bearer" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid Authorization header format"})
			}

			apiKey := authHeaderParts[1]
			if apiKey != options.apiKey {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid API key"})
			}
		}
		return c.Next()
	}

	if options.preloadJSONModels != "" {
		if err := ApplyGalleryFromString(options.loader.ModelPath, options.preloadJSONModels, cm, options.galleries); err != nil {
			return nil, err
		}
	}

	if options.preloadModelsFromPath != "" {
		if err := ApplyGalleryFromFile(options.loader.ModelPath, options.preloadModelsFromPath, cm, options.galleries); err != nil {
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

	app.Get("/version", auth, func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	app.Post("/models/apply", auth, applyModelGallery(options.loader.ModelPath, cm, applier.C, options.galleries))
	app.Get("/models/available", auth, listModelFromGallery(options.galleries, options.loader.ModelPath))
	app.Get("/models/jobs/:uuid", auth, getOpStatus(applier))

	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", auth, chatEndpoint(cm, options))
	app.Post("/chat/completions", auth, chatEndpoint(cm, options))

	// edit
	app.Post("/v1/edits", auth, editEndpoint(cm, options))
	app.Post("/edits", auth, editEndpoint(cm, options))

	// completion
	app.Post("/v1/completions", auth, completionEndpoint(cm, options))
	app.Post("/completions", auth, completionEndpoint(cm, options))
	app.Post("/v1/engines/:model/completions", auth, completionEndpoint(cm, options))

	// embeddings
	app.Post("/v1/embeddings", auth, embeddingsEndpoint(cm, options))
	app.Post("/embeddings", auth, embeddingsEndpoint(cm, options))
	app.Post("/v1/engines/:model/embeddings", auth, embeddingsEndpoint(cm, options))

	// audio
	app.Post("/v1/audio/transcriptions", auth, transcriptEndpoint(cm, options))
	app.Post("/tts", auth, ttsEndpoint(cm, options))

	// images
	app.Post("/v1/images/generations", auth, imageEndpoint(cm, options))

	if options.imageDir != "" {
		app.Static("/generated-images", options.imageDir)
	}

	if options.audioDir != "" {
		app.Static("/generated-audio", options.audioDir)
	}

	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	// Kubernetes health checks
	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	// models
	app.Get("/v1/models", auth, listModels(options.loader, cm))
	app.Get("/models", auth, listModels(options.loader, cm))

	return app, nil
}
