package routes

import (
	"context"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/ollama"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
)

func RegisterOllamaRoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	application *application.Application) {

	traceMiddleware := middleware.TraceMiddleware(application)
	usageMiddleware := middleware.UsageMiddleware(application.AuthDB())

	// Chat endpoint: POST /api/chat
	chatHandler := ollama.ChatEndpoint(
		application.ModelConfigLoader(),
		application.ModelLoader(),
		application.TemplatesEvaluator(),
		application.ApplicationConfig(),
	)
	chatMiddleware := []echo.MiddlewareFunc{
		usageMiddleware,
		traceMiddleware,
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OllamaChatRequest) }),
		setOllamaChatRequestContext(application.ApplicationConfig()),
	}
	app.POST("/api/chat", chatHandler, chatMiddleware...)

	// Generate endpoint: POST /api/generate
	generateHandler := ollama.GenerateEndpoint(
		application.ModelConfigLoader(),
		application.ModelLoader(),
		application.TemplatesEvaluator(),
		application.ApplicationConfig(),
	)
	generateMiddleware := []echo.MiddlewareFunc{
		usageMiddleware,
		traceMiddleware,
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OllamaGenerateRequest) }),
		setOllamaGenerateRequestContext(application.ApplicationConfig()),
	}
	app.POST("/api/generate", generateHandler, generateMiddleware...)

	// Embed endpoints: POST /api/embed and /api/embeddings
	embedHandler := ollama.EmbedEndpoint(
		application.ModelConfigLoader(),
		application.ModelLoader(),
		application.ApplicationConfig(),
	)
	embedMiddleware := []echo.MiddlewareFunc{
		usageMiddleware,
		traceMiddleware,
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EMBEDDINGS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OllamaEmbedRequest) }),
	}
	app.POST("/api/embed", embedHandler, embedMiddleware...)
	app.POST("/api/embeddings", embedHandler, embedMiddleware...)

	// Model management endpoints (no model-specific middleware needed)
	app.GET("/api/tags", ollama.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader()))
	app.HEAD("/api/tags", ollama.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader()))
	app.POST("/api/show", ollama.ShowModelEndpoint(application.ModelConfigLoader()))
	app.GET("/api/ps", ollama.ListRunningEndpoint(application.ModelConfigLoader(), application.ModelLoader()))
	app.GET("/api/version", ollama.VersionEndpoint())
	app.HEAD("/api/version", ollama.VersionEndpoint())
}

// RegisterOllamaRootEndpoint registers the Ollama "/" health check.
// This is separate because it conflicts with the web UI and is gated behind a CLI flag.
func RegisterOllamaRootEndpoint(app *echo.Echo) {
	app.GET("/", ollama.HeartbeatEndpoint())
	app.HEAD("/", ollama.HeartbeatEndpoint())
}

// setOllamaChatRequestContext sets up context and cancellation for Ollama chat requests
func setOllamaChatRequestContext(appConfig *config.ApplicationConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OllamaChatRequest)
			if !ok || input.Model == "" {
				return echo.ErrBadRequest
			}

			cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			if !ok || cfg == nil {
				return echo.ErrBadRequest
			}

			correlationID := uuid.New().String()
			c.Response().Header().Set("X-Correlation-ID", correlationID)

			reqCtx := c.Request().Context()
			c1, cancel := context.WithCancel(appConfig.Context)
			stop := context.AfterFunc(reqCtx, cancel)
			defer func() {
				stop()
				cancel()
			}()

			ctxWithCorrelationID := context.WithValue(c1, middleware.CorrelationIDKey, correlationID)
			input.Context = ctxWithCorrelationID
			input.Cancel = cancel

			if cfg.Model == "" {
				cfg.Model = input.Model
			}

			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

			return next(c)
		}
	}
}

// setOllamaGenerateRequestContext sets up context and cancellation for Ollama generate requests
func setOllamaGenerateRequestContext(appConfig *config.ApplicationConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OllamaGenerateRequest)
			if !ok || input.Model == "" {
				return echo.ErrBadRequest
			}

			cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			if !ok || cfg == nil {
				return echo.ErrBadRequest
			}

			correlationID := uuid.New().String()
			c.Response().Header().Set("X-Correlation-ID", correlationID)

			reqCtx := c.Request().Context()
			c1, cancel := context.WithCancel(appConfig.Context)
			stop := context.AfterFunc(reqCtx, cancel)
			defer func() {
				stop()
				cancel()
			}()

			ctxWithCorrelationID := context.WithValue(c1, middleware.CorrelationIDKey, correlationID)
			input.Ctx = ctxWithCorrelationID
			input.Cancel = cancel

			if cfg.Model == "" {
				cfg.Model = input.Model
			}

			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

			return next(c)
		}
	}
}
