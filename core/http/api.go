package http

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/go-skynet/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/go-skynet/LocalAI/core/http/endpoints/localai"
	"github.com/go-skynet/LocalAI/core/http/endpoints/openai"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/model"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
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

func App(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) (*fiber.App, error) {
	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
		BodyLimit:             appConfig.UploadLimitMB * 1024 * 1024, // this is the default limit of 4MB
		DisableStartupMessage: appConfig.DisableMessage,
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

	if appConfig.Debug {
		app.Use(logger.New(logger.Config{
			Format: "[${ip}]:${port} ${status} - ${method} ${path}\n",
		}))
	}

	// Default middleware config

	if !appConfig.Debug {
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
		if len(appConfig.ApiKeys) == 0 {
			return c.Next()
		}

		// Check for api_keys.json file
		fileContent, err := os.ReadFile("api_keys.json")
		if err == nil {
			// Parse JSON content from the file
			var fileKeys []string
			err := json.Unmarshal(fileContent, &fileKeys)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"message": "Error parsing api_keys.json"})
			}

			// Add file keys to options.ApiKeys
			appConfig.ApiKeys = append(appConfig.ApiKeys, fileKeys...)
		}

		if len(appConfig.ApiKeys) == 0 {
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
		for _, key := range appConfig.ApiKeys {
			if apiKey == key {
				return c.Next()
			}
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid API key"})
	}

	if appConfig.CORS {
		var c func(ctx *fiber.Ctx) error
		if appConfig.CORSAllowOrigins == "" {
			c = cors.New()
		} else {
			c = cors.New(cors.Config{AllowOrigins: appConfig.CORSAllowOrigins})
		}

		app.Use(c)
	}

	// LocalAI API endpoints
	galleryService := services.NewGalleryService(appConfig.ModelPath)
	galleryService.Start(appConfig.Context, cl)

	app.Get("/version", auth, func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	// Load upload json
	openai.LoadUploadConfig(appConfig.UploadDir)

	modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(appConfig.Galleries, appConfig.ModelPath, galleryService)
	app.Post("/models/apply", auth, modelGalleryEndpointService.ApplyModelGalleryEndpoint())
	app.Get("/models/available", auth, modelGalleryEndpointService.ListModelFromGalleryEndpoint())
	app.Get("/models/galleries", auth, modelGalleryEndpointService.ListModelGalleriesEndpoint())
	app.Post("/models/galleries", auth, modelGalleryEndpointService.AddModelGalleryEndpoint())
	app.Delete("/models/galleries", auth, modelGalleryEndpointService.RemoveModelGalleryEndpoint())
	app.Get("/models/jobs/:uuid", auth, modelGalleryEndpointService.GetOpStatusEndpoint())
	app.Get("/models/jobs", auth, modelGalleryEndpointService.GetAllStatusEndpoint())

	app.Post("/tts", auth, localai.TTSEndpoint(cl, ml, appConfig))

	// Elevenlabs
	app.Post("/v1/text-to-speech/:voice-id", auth, elevenlabs.TTSEndpoint(cl, ml, appConfig))

	// Stores
	sl := model.NewModelLoader("")
	app.Post("/stores/set", auth, localai.StoresSetEndpoint(sl, appConfig))
	app.Post("/stores/delete", auth, localai.StoresDeleteEndpoint(sl, appConfig))
	app.Post("/stores/get", auth, localai.StoresGetEndpoint(sl, appConfig))
	app.Post("/stores/find", auth, localai.StoresFindEndpoint(sl, appConfig))

	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", auth, openai.ChatEndpoint(cl, ml, appConfig))
	app.Post("/chat/completions", auth, openai.ChatEndpoint(cl, ml, appConfig))

	// edit
	app.Post("/v1/edits", auth, openai.EditEndpoint(cl, ml, appConfig))
	app.Post("/edits", auth, openai.EditEndpoint(cl, ml, appConfig))

	// files
	app.Post("/v1/files", auth, openai.UploadFilesEndpoint(cl, appConfig))
	app.Post("/files", auth, openai.UploadFilesEndpoint(cl, appConfig))
	app.Get("/v1/files", auth, openai.ListFilesEndpoint(cl, appConfig))
	app.Get("/files", auth, openai.ListFilesEndpoint(cl, appConfig))
	app.Get("/v1/files/:file_id", auth, openai.GetFilesEndpoint(cl, appConfig))
	app.Get("/files/:file_id", auth, openai.GetFilesEndpoint(cl, appConfig))
	app.Delete("/v1/files/:file_id", auth, openai.DeleteFilesEndpoint(cl, appConfig))
	app.Delete("/files/:file_id", auth, openai.DeleteFilesEndpoint(cl, appConfig))
	app.Get("/v1/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(cl, appConfig))
	app.Get("/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(cl, appConfig))

	// completion
	app.Post("/v1/completions", auth, openai.CompletionEndpoint(cl, ml, appConfig))
	app.Post("/completions", auth, openai.CompletionEndpoint(cl, ml, appConfig))
	app.Post("/v1/engines/:model/completions", auth, openai.CompletionEndpoint(cl, ml, appConfig))

	// embeddings
	app.Post("/v1/embeddings", auth, openai.EmbeddingsEndpoint(cl, ml, appConfig))
	app.Post("/embeddings", auth, openai.EmbeddingsEndpoint(cl, ml, appConfig))
	app.Post("/v1/engines/:model/embeddings", auth, openai.EmbeddingsEndpoint(cl, ml, appConfig))

	// audio
	app.Post("/v1/audio/transcriptions", auth, openai.TranscriptEndpoint(cl, ml, appConfig))
	app.Post("/v1/audio/speech", auth, localai.TTSEndpoint(cl, ml, appConfig))

	// images
	app.Post("/v1/images/generations", auth, openai.ImageEndpoint(cl, ml, appConfig))

	if appConfig.ImageDir != "" {
		app.Static("/generated-images", appConfig.ImageDir)
	}

	if appConfig.AudioDir != "" {
		app.Static("/generated-audio", appConfig.AudioDir)
	}

	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	// Kubernetes health checks
	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	// Experimental Backend Statistics Module
	backendMonitor := services.NewBackendMonitor(cl, ml, appConfig) // Split out for now
	app.Get("/backend/monitor", localai.BackendMonitorEndpoint(backendMonitor))
	app.Post("/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitor))

	// models
	app.Get("/v1/models", auth, openai.ListModelsEndpoint(cl, ml))
	app.Get("/models", auth, openai.ListModelsEndpoint(cl, ml))

	app.Get("/metrics", localai.LocalAIMetricsEndpoint())

	return app, nil
}
