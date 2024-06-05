package http

import (
	"embed"
	"errors"
	"net/http"

	"github.com/go-skynet/LocalAI/core"
	"github.com/go-skynet/LocalAI/pkg/utils"

	"github.com/go-skynet/LocalAI/core/http/endpoints/openai"
	"github.com/go-skynet/LocalAI/core/http/middleware"
	"github.com/go-skynet/LocalAI/core/http/routes"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"

	"github.com/gofiber/contrib/fiberzerolog"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/csrf"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/rs/zerolog/log"
)

// Embed a directory
//
//go:embed static/*
var embedDirStatic embed.FS

// @title LocalAI API
// @version 2.0.0
// @description The LocalAI Rest API.
// @termsOfService
// @contact.name LocalAI
// @contact.url https://localai.io
// @license.name MIT
// @license.url https://raw.githubusercontent.com/mudler/LocalAI/master/LICENSE
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func App(application *core.Application) (*fiber.App, error) {
	fiberCfg := fiber.Config{
		Views:     renderEngine(),
		BodyLimit: application.ApplicationConfig.UploadLimitMB * 1024 * 1024, // this is the default limit of 4MB
		// We disable the Fiber startup message as it does not conform to structured logging.
		// We register a startup log line with connection information in the OnListen hook to keep things user friendly though
		DisableStartupMessage: true,
		// Override default error handler
	}

	if !application.ApplicationConfig.OpaqueErrors {
		// Normally, return errors as JSON responses
		fiberCfg.ErrorHandler = func(ctx *fiber.Ctx, err error) error {
			// Status code defaults to 500
			code := fiber.StatusInternalServerError

			// Retrieve the custom status code if it's a *fiber.Error
			var e *fiber.Error
			if errors.As(err, &e) {
				code = e.Code
			}

			// Send custom error page
			return ctx.Status(code).JSON(
				schema.ErrorResponse{
					Error: &schema.APIError{Message: err.Error(), Code: code},
				},
			)
		}
	} else {
		// If OpaqueErrors are required, replace everything with a blank 500.
		fiberCfg.ErrorHandler = func(ctx *fiber.Ctx, _ error) error {
			return ctx.Status(500).SendString("")
		}
	}

	app := fiber.New(fiberCfg)

	app.Hooks().OnListen(func(listenData fiber.ListenData) error {
		scheme := "http"
		if listenData.TLS {
			scheme = "https"
		}
		log.Info().Str("endpoint", scheme+"://"+listenData.Host+":"+listenData.Port).Msg("LocalAI API is listening! Please connect to the endpoint for API documentation.")
		return nil
	})

	// Have Fiber use zerolog like the rest of the application rather than it's built-in logger
	logger := log.Logger
	app.Use(fiberzerolog.New(fiberzerolog.Config{
		Logger: &logger,
	}))

	// Default middleware config

	if !application.ApplicationConfig.Debug {
		app.Use(recover.New())
	}

	metricsService, err := services.NewLocalAIMetricsService()
	if err != nil {
		return nil, err
	}

	if metricsService != nil {
		app.Use(middleware.GetMetrics(metricsService))
		app.Hooks().OnShutdown(func() error {
			return metricsService.Shutdown()
		})
	}

	auth := middleware.GetAuth(application.ApplicationConfig)
	requestExtractor := middleware.NewRequestExtractor(application.ModelLoader, application.ApplicationConfig)

	if application.ApplicationConfig.CORS {
		var c func(ctx *fiber.Ctx) error
		if application.ApplicationConfig.CORSAllowOrigins == "" {
			c = cors.New()
		} else {
			c = cors.New(cors.Config{AllowOrigins: application.ApplicationConfig.CORSAllowOrigins})
		}

		app.Use(c)
	}

	if application.ApplicationConfig.CSRF {
		log.Debug().Msg("Enabling CSRF middleware. Tokens are now required for state-modifying requests")
		app.Use(csrf.New())
	}

	// Load config jsons
	utils.LoadConfig(application.ApplicationConfig.UploadDir, openai.UploadedFilesFile, &openai.UploadedFiles)
	utils.LoadConfig(application.ApplicationConfig.ConfigsDir, openai.AssistantsConfigFile, &openai.Assistants)
	utils.LoadConfig(application.ApplicationConfig.ConfigsDir, openai.AssistantsFileConfigFile, &openai.AssistantFiles)

	// Register all routes - TODO: enhance for partial registration?
	// For the "large" register function, it seems to make sense to pass application directly and allow them to sort out their dependencies.
	// However, for particularly simple routes, passing dependencies directly may be more clean? Try both and experiment!
	routes.RegisterElevenLabsRoutes(app, application.TextToSpeechBackendService, requestExtractor, auth)
	routes.RegisterLocalAIRoutes(app, application, requestExtractor, auth)
	routes.RegisterOpenAIRoutes(app, application, requestExtractor, auth)
	routes.RegisterJINARoutes(app, application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig, auth)

	if !application.ApplicationConfig.DisableWebUI {
		routes.RegisterUIRoutes(app, application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig, application.GalleryService, auth)
	}

	httpFS := http.FS(embedDirStatic)

	app.Use(favicon.New(favicon.Config{
		URL:        "/favicon.ico",
		FileSystem: httpFS,
		File:       "static/favicon.ico",
	}))

	app.Use("/static", filesystem.New(filesystem.Config{
		Root:       httpFS,
		PathPrefix: "static",
		Browse:     true,
	}))

	// Define a custom 404 handler
	// Note: keep this at the bottom!
	app.Use(notFoundHandler)

	return app, nil
}
