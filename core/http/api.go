package http

import (
	"errors"
	"strings"

	"github.com/go-skynet/LocalAI/core"
	fiberContext "github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/http/endpoints/localai"
	"github.com/go-skynet/LocalAI/core/http/endpoints/openai"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/internal"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

func App(application *core.Application) (*fiber.App, error) {
	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
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

		// // Check for api_keys.json file
		// fileContent, err := os.ReadFile("api_keys.json")
		// if err == nil {
		// 	// Parse JSON content from the file
		// 	var fileKeys []string
		// 	err := json.Unmarshal(fileContent, &fileKeys)
		// 	if err != nil {
		// 		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Error parsing api_keys.json"})
		// 	}

		// 	// Add file keys to options.ApiKeys
		// 	application.ApplicationConfig.ApiKeys = append(application.ApplicationConfig.ApiKeys, fileKeys...)
		// }

		// if len(application.ApplicationConfig.ApiKeys) == 0 {
		// 	return c.Next()
		// }

		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Authorization header missing"})
		}
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

	fiberContextExtractor := fiberContext.NewFiberContextExtractor(application.ModelLoader, application.ApplicationConfig)

	// LocalAI API endpoints
	galleryService := services.NewGalleryService(application.ApplicationConfig.ModelPath)
	galleryService.Start(application.ApplicationConfig.Context, application.BackendConfigLoader)

	app.Get("/version", auth, func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	// Load upload json
	openai.LoadUploadConfig(application.ApplicationConfig.UploadDir)

	modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(application.ApplicationConfig.Galleries, application.ApplicationConfig.ModelPath, galleryService)
	app.Post("/models/apply", auth, modelGalleryEndpointService.ApplyModelGalleryEndpoint())
	app.Get("/models/available", auth, modelGalleryEndpointService.ListModelFromGalleryEndpoint())
	app.Get("/models/galleries", auth, modelGalleryEndpointService.ListModelGalleriesEndpoint())
	app.Post("/models/galleries", auth, modelGalleryEndpointService.AddModelGalleryEndpoint())
	app.Delete("/models/galleries", auth, modelGalleryEndpointService.RemoveModelGalleryEndpoint())
	app.Get("/models/jobs/:uuid", auth, modelGalleryEndpointService.GetOpStatusEndpoint())
	app.Get("/models/jobs", auth, modelGalleryEndpointService.GetAllStatusEndpoint())

	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", auth, openai.ChatEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/chat/completions", auth, openai.ChatEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	// edit
	app.Post("/v1/edits", auth, openai.EditEndpoint(fiberContextExtractor, application.OpenAIService))
	app.Post("/edits", auth, openai.EditEndpoint(fiberContextExtractor, application.OpenAIService))

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
	app.Post("/tts", auth, localai.TTSEndpoint(fiberContextExtractor, application.TextToSpeechBackendService))

	// images
	app.Post("/v1/images/generations", auth, openai.ImageEndpoint(fiberContextExtractor, application.ImageGenerationBackendService))

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
	app.Get("/backend/monitor", localai.BackendMonitorEndpoint(application.BackendMonitorService))
	app.Post("/backend/shutdown", localai.BackendShutdownEndpoint(application.BackendMonitorService))

	// models
	app.Get("/v1/models", auth, openai.ListModelsEndpoint(application.ListModelsService))
	app.Get("/models", auth, openai.ListModelsEndpoint(application.ListModelsService))

	app.Get("/metrics", localai.LocalAIMetricsEndpoint())

	return app, nil
}
