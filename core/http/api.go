package http

import (
	"errors"
	"strings"

	"github.com/go-skynet/LocalAI/core"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/gofiber/swagger" // swagger handler

	"github.com/go-skynet/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/go-skynet/LocalAI/core/http/endpoints/localai"
	"github.com/go-skynet/LocalAI/core/http/endpoints/openai"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/model"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/rs/zerolog/log"
)

func readAuthHeader(c *fiber.Ctx) string {
	authHeader := c.Get("Authorization")

	// elevenlabs
	xApiKey := c.Get("xi-api-key")
	if xApiKey != "" {
		authHeader = "Bearer " + xApiKey
	}

	// anthropic
	xApiKey = c.Get("x-api-key")
	if xApiKey != "" {
		authHeader = "Bearer " + xApiKey
	}

	return authHeader
}

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
	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
		Views:                 renderEngine(),
		BodyLimit:             application.ApplicationConfig.UploadLimitMB * 1024 * 1024, // this is the default limit of 4MB
		DisableStartupMessage: application.ApplicationConfig.DisableMessage,
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
				schema.ErrorResponse{
					Error: &schema.APIError{Message: err.Error(), Code: code},
				},
			)
		},
	})

	if application.ApplicationConfig.Debug {
		app.Use(logger.New(logger.Config{
			Format: "[${ip}]:${port} ${status} - ${method} ${path}\n",
		}))
	}

	// Default middleware config

	if !application.ApplicationConfig.Debug {
		app.Use(recover.New())
	}

	metricsService, err := services.NewLocalAIMetricsService()
	if err != nil {
		return nil, err
	}

	if metricsService != nil {
		app.Use(localai.LocalAIMetricsAPIMiddleware(metricsService))
		app.Hooks().OnShutdown(func() error {
			return metricsService.Shutdown()
		})
	}

	// Auth middleware checking if API key is valid. If no API key is set, no auth is required.
	auth := func(c *fiber.Ctx) error {
		if len(application.ApplicationConfig.ApiKeys) == 0 {
			return c.Next()
		}

		authHeader := readAuthHeader(c)
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Authorization header missing"})
		}

		// If it's a bearer token
		authHeaderParts := strings.Split(authHeader, " ")
		if len(authHeaderParts) != 2 || authHeaderParts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid Authorization header format"})
		}

		apiKey := authHeaderParts[1]
		for _, key := range application.ApplicationConfig.ApiKeys {
			if apiKey == key {
				return c.Next()
			}
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid API key"})
	}

	if application.ApplicationConfig.CORS {
		var c func(ctx *fiber.Ctx) error
		if application.ApplicationConfig.CORSAllowOrigins == "" {
			c = cors.New()
		} else {
			c = cors.New(cors.Config{AllowOrigins: application.ApplicationConfig.CORSAllowOrigins})
		}

		app.Use(c)
	}

	if application.ApplicationConfig.EnableDynamicRouting {
		dynamicRoutingMW := func(ctx *fiber.Ctx) error {
			log.Debug().Msg("TOP OF DYNAMIC ROUTING MIDDLEWARE!")
			var anyBody interface{} // Not sure this works, may need a lookup of endpoint to request type here, does mean we can go generic :D
			initialPath := ctx.Path()
			if err := ctx.BodyParser(anyBody); err != nil {
				log.Error().Msgf("[DYNAMIC ROUTING ERROR] %q", err)
				return err
			}
			destination, endpoint, request, err := application.RequestRoutingService.RouteRequest("http", initialPath, anyBody)
			if err != nil {
				log.Error().Msgf("[Dynamic Routing Rules Error] %q", err)
				return err
			}
			if destination != "http" {
				log.Warn().Msgf("!!!!!!! Temporary Log: NonHTTP destination %q endpoint: %q %+v", destination, endpoint, request)
			}
			if endpoint != initialPath {
				log.Debug().Msgf("[Dynamic Routing] Overriding Path from %q to %q", initialPath, endpoint)
				ctx.Path(endpoint)
			}
			return ctx.Next()
		}
		app.Use(dynamicRoutingMW)
	}

	fiberContextExtractor := fiberContext.NewFiberContextExtractor(application.ModelLoader, application.ApplicationConfig)

	// LocalAI API endpoints
	galleryService := services.NewGalleryService(application.ApplicationConfig.ModelPath)
	galleryService.Start(application.ApplicationConfig.Context, application.BackendConfigLoader)

	app.Get("/version", auth, func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	app.Get("/swagger/*", swagger.HandlerDefault) // default

	welcomeRoute(
		app,
		application.BackendConfigLoader,
		application.ModelLoader,
		application.ApplicationConfig,
		auth,
	)

	modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(application.ApplicationConfig.Galleries, application.ApplicationConfig.ModelPath, galleryService)
	app.Post("/models/apply", auth, modelGalleryEndpointService.ApplyModelGalleryEndpoint())
	app.Get("/models/available", auth, modelGalleryEndpointService.ListModelFromGalleryEndpoint())
	app.Get("/models/galleries", auth, modelGalleryEndpointService.ListModelGalleriesEndpoint())
	app.Post("/models/galleries", auth, modelGalleryEndpointService.AddModelGalleryEndpoint())
	app.Delete("/models/galleries", auth, modelGalleryEndpointService.RemoveModelGalleryEndpoint())
	app.Get("/models/jobs/:uuid", auth, modelGalleryEndpointService.GetOpStatusEndpoint())
	app.Get("/models/jobs", auth, modelGalleryEndpointService.GetAllStatusEndpoint())

	// Stores
	storeLoader := model.NewModelLoader("") // TODO: Investigate if this should be migrated to application and reused. Should the path be configurable? Merging for now.
	app.Post("/stores/set", auth, localai.StoresSetEndpoint(storeLoader, application.ApplicationConfig))
	app.Post("/stores/delete", auth, localai.StoresDeleteEndpoint(storeLoader, application.ApplicationConfig))
	app.Post("/stores/get", auth, localai.StoresGetEndpoint(storeLoader, application.ApplicationConfig))
	app.Post("/stores/find", auth, localai.StoresFindEndpoint(storeLoader, application.ApplicationConfig))

	// openAI compatible API endpoints

	// chat
	app.Post("/v1/chat/completions", auth, openai.ChatEndpoint(fiberContextExtractor, application.OpenAIService))
	app.Post("/chat/completions", auth, openai.ChatEndpoint(fiberContextExtractor, application.OpenAIService))

	// edit
	app.Post("/v1/edits", auth, openai.EditEndpoint(fiberContextExtractor, application.OpenAIService))
	app.Post("/edits", auth, openai.EditEndpoint(fiberContextExtractor, application.OpenAIService))

	// assistant
	// TODO: Refactor this to the new style eventually
	app.Get("/v1/assistants", auth, openai.ListAssistantsEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants", auth, openai.ListAssistantsEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants", auth, openai.CreateAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants", auth, openai.CreateAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/v1/assistants/:assistant_id", auth, openai.DeleteAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/assistants/:assistant_id", auth, openai.DeleteAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id", auth, openai.GetAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id", auth, openai.GetAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants/:assistant_id", auth, openai.ModifyAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants/:assistant_id", auth, openai.ModifyAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id/files", auth, openai.ListAssistantFilesEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id/files", auth, openai.ListAssistantFilesEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants/:assistant_id/files", auth, openai.CreateAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants/:assistant_id/files", auth, openai.CreateAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/v1/assistants/:assistant_id/files/:file_id", auth, openai.DeleteAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/assistants/:assistant_id/files/:file_id", auth, openai.DeleteAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id/files/:file_id", auth, openai.GetAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id/files/:file_id", auth, openai.GetAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	// files
	app.Post("/v1/files", auth, openai.UploadFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Post("/files", auth, openai.UploadFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files", auth, openai.ListFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files", auth, openai.ListFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files/:file_id", auth, openai.GetFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files/:file_id", auth, openai.GetFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Delete("/v1/files/:file_id", auth, openai.DeleteFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Delete("/files/:file_id", auth, openai.DeleteFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(application.BackendConfigLoader, application.ApplicationConfig))

	// completion
	app.Post("/v1/completions", auth, openai.CompletionEndpoint(fiberContextExtractor, application.OpenAIService))
	app.Post("/completions", auth, openai.CompletionEndpoint(fiberContextExtractor, application.OpenAIService))
	app.Post("/v1/engines/:model/completions", auth, openai.CompletionEndpoint(fiberContextExtractor, application.OpenAIService))

	// embeddings
	app.Post("/v1/embeddings", auth, openai.EmbeddingsEndpoint(fiberContextExtractor, application.EmbeddingsBackendService))
	app.Post("/embeddings", auth, openai.EmbeddingsEndpoint(fiberContextExtractor, application.EmbeddingsBackendService))
	app.Post("/v1/engines/:model/embeddings", auth, openai.EmbeddingsEndpoint(fiberContextExtractor, application.EmbeddingsBackendService))

	// audio
	app.Post("/v1/audio/transcriptions", auth, openai.TranscriptEndpoint(fiberContextExtractor, application.TranscriptionBackendService))
	app.Post("/v1/audio/speech", auth, localai.TTSEndpoint(fiberContextExtractor, application.TextToSpeechBackendService))

	// images
	app.Post("/v1/images/generations", auth, openai.ImageEndpoint(fiberContextExtractor, application.ImageGenerationBackendService))

	// Elevenlabs
	app.Post("/v1/text-to-speech/:voice-id", auth, elevenlabs.TTSEndpoint(fiberContextExtractor, application.TextToSpeechBackendService))

	// LocalAI TTS?
	app.Post("/tts", auth, localai.TTSEndpoint(fiberContextExtractor, application.TextToSpeechBackendService))

	if application.ApplicationConfig.ImageDir != "" {
		app.Static("/generated-images", application.ApplicationConfig.ImageDir)
	}

	if application.ApplicationConfig.AudioDir != "" {
		app.Static("/generated-audio", application.ApplicationConfig.AudioDir)
	}

	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	// Kubernetes health checks
	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	// Experimental Backend Statistics Module
	app.Get("/backend/monitor", auth, localai.BackendMonitorEndpoint(application.BackendMonitorService))
	app.Post("/backend/shutdown", auth, localai.BackendShutdownEndpoint(application.BackendMonitorService))

	// models
	app.Get("/v1/models", auth, openai.ListModelsEndpoint(application.ListModelsService))
	app.Get("/models", auth, openai.ListModelsEndpoint(application.ListModelsService))

	app.Get("/metrics", auth, localai.LocalAIMetricsEndpoint())

	// Define a custom 404 handler
	// Note: keep this at the bottom!
	app.Use(notFoundHandler)

	return app, nil
}
