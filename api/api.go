package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/localai"
	"github.com/go-skynet/LocalAI/api/openai"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/api/schema"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/metrics"
	"github.com/go-skynet/LocalAI/pkg/assets"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/startup"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Startup(opts ...options.AppOption) (*options.Option, *config.ConfigLoader, error) {
	options := options.NewOptions(opts...)

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if options.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", options.Threads, options.Loader.ModelPath)
	log.Info().Msgf("LocalAI version: %s", internal.PrintableVersion())

	startup.PreloadModelsConfigurations(options.ModelLibraryURL, options.Loader.ModelPath, options.ModelsURL...)

	cl := config.NewConfigLoader()
	if err := cl.LoadConfigs(options.Loader.ModelPath); err != nil {
		log.Error().Msgf("error loading config files: %s", err.Error())
	}

	if options.ConfigFile != "" {
		if err := cl.LoadConfigFile(options.ConfigFile); err != nil {
			log.Error().Msgf("error loading config file: %s", err.Error())
		}
	}

	if err := cl.Preload(options.Loader.ModelPath); err != nil {
		log.Error().Msgf("error downloading models: %s", err.Error())
	}

	if options.PreloadJSONModels != "" {
		if err := localai.ApplyGalleryFromString(options.Loader.ModelPath, options.PreloadJSONModels, cl, options.Galleries); err != nil {
			return nil, nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := localai.ApplyGalleryFromFile(options.Loader.ModelPath, options.PreloadModelsFromPath, cl, options.Galleries); err != nil {
			return nil, nil, err
		}
	}

	if options.Debug {
		for _, v := range cl.ListConfigs() {
			cfg, _ := cl.GetConfig(v)
			log.Debug().Msgf("Model: %s (config: %+v)", v, cfg)
		}
	}

	if options.AssetsDestination != "" {
		// Extract files from the embedded FS
		err := assets.ExtractFiles(options.BackendAssets, options.AssetsDestination)
		log.Debug().Msgf("Extracting backend assets files to %s", options.AssetsDestination)
		if err != nil {
			log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly, like gpt4all)", err)
		}
	}

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-options.Context.Done()
		log.Debug().Msgf("Context canceled, shutting down")
		options.Loader.StopAllGRPC()
	}()

	if options.WatchDog {
		wd := model.NewWatchDog(
			options.Loader,
			options.WatchDogBusyTimeout,
			options.WatchDogIdleTimeout,
			options.WatchDogBusy,
			options.WatchDogIdle)
		options.Loader.SetWatchDog(wd)
		go wd.Run()
		go func() {
			<-options.Context.Done()
			log.Debug().Msgf("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}

	return options, cl, nil
}

func App(opts ...options.AppOption) (*fiber.App, error) {

	options, cl, err := Startup(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed basic startup tasks with error %s", err.Error())
	}

	// Return errors as JSON responses
	app := fiber.New(fiber.Config{
		BodyLimit:             options.UploadLimitMB * 1024 * 1024, // this is the default limit of 4MB
		DisableStartupMessage: options.DisableMessage,
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

	if options.Debug {
		app.Use(logger.New(logger.Config{
			Format: "[${ip}]:${port} ${status} - ${method} ${path}\n",
		}))
	}

	// Default middleware config
	app.Use(recover.New())
	if options.Metrics != nil {
		app.Use(metrics.APIMiddleware(options.Metrics))
	}

	// Auth middleware checking if API key is valid. If no API key is set, no auth is required.
	auth := func(c *fiber.Ctx) error {
		if len(options.ApiKeys) == 0 {
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
			options.ApiKeys = append(options.ApiKeys, fileKeys...)
		}

		if len(options.ApiKeys) == 0 {
			return c.Next()
		}

		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Authorization header missing"})
		}
		authHeaderParts := strings.Split(authHeader, " ")
		if len(authHeaderParts) != 2 || authHeaderParts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid Authorization header format"})
		}

		apiKey := authHeaderParts[1]
		for _, key := range options.ApiKeys {
			if apiKey == key {
				return c.Next()
			}
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"message": "Invalid API key"})

	}

	if options.CORS {
		var c func(ctx *fiber.Ctx) error
		if options.CORSAllowOrigins == "" {
			c = cors.New()
		} else {
			c = cors.New(cors.Config{AllowOrigins: options.CORSAllowOrigins})
		}

		app.Use(c)
	}

	// LocalAI API endpoints
	galleryService := localai.NewGalleryService(options.Loader.ModelPath)
	galleryService.Start(options.Context, cl)

	app.Get("/version", auth, func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	// Make sure directories exists
	os.MkdirAll(options.ImageDir, 0755)
	os.MkdirAll(options.AudioDir, 0755)
	os.MkdirAll(options.Loader.ModelPath, 0755)

	modelGalleryService := localai.CreateModelGalleryService(options.Galleries, options.Loader.ModelPath, galleryService)
	app.Post("/models/apply", auth, modelGalleryService.ApplyModelGalleryEndpoint())
	app.Get("/models/available", auth, modelGalleryService.ListModelFromGalleryEndpoint())
	app.Get("/models/galleries", auth, modelGalleryService.ListModelGalleriesEndpoint())
	app.Post("/models/galleries", auth, modelGalleryService.AddModelGalleryEndpoint())
	app.Delete("/models/galleries", auth, modelGalleryService.RemoveModelGalleryEndpoint())
	app.Get("/models/jobs/:uuid", auth, modelGalleryService.GetOpStatusEndpoint())
	app.Get("/models/jobs", auth, modelGalleryService.GetAllStatusEndpoint())

	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", auth, openai.ChatEndpoint(cl, options))
	app.Post("/chat/completions", auth, openai.ChatEndpoint(cl, options))

	// edit
	app.Post("/v1/edits", auth, openai.EditEndpoint(cl, options))
	app.Post("/edits", auth, openai.EditEndpoint(cl, options))

	// completion
	app.Post("/v1/completions", auth, openai.CompletionEndpoint(cl, options))
	app.Post("/completions", auth, openai.CompletionEndpoint(cl, options))
	app.Post("/v1/engines/:model/completions", auth, openai.CompletionEndpoint(cl, options))

	// embeddings
	app.Post("/v1/embeddings", auth, openai.EmbeddingsEndpoint(cl, options))
	app.Post("/embeddings", auth, openai.EmbeddingsEndpoint(cl, options))
	app.Post("/v1/engines/:model/embeddings", auth, openai.EmbeddingsEndpoint(cl, options))

	// audio
	app.Post("/v1/audio/transcriptions", auth, openai.TranscriptEndpoint(cl, options))
	app.Post("/tts", auth, localai.TTSEndpoint(cl, options))

	// images
	app.Post("/v1/images/generations", auth, openai.ImageEndpoint(cl, options))

	if options.ImageDir != "" {
		app.Static("/generated-images", options.ImageDir)
	}

	if options.AudioDir != "" {
		app.Static("/generated-audio", options.AudioDir)
	}

	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	// Kubernetes health checks
	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	// Experimental Backend Statistics Module
	backendMonitor := localai.NewBackendMonitor(cl, options) // Split out for now
	app.Get("/backend/monitor", localai.BackendMonitorEndpoint(backendMonitor))
	app.Post("/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitor))

	// models
	app.Get("/v1/models", auth, openai.ListModelsEndpoint(options.Loader, cl))
	app.Get("/models", auth, openai.ListModelsEndpoint(options.Loader, cl))

	app.Get("/metrics", metrics.MetricsHandler())

	return app, nil
}
