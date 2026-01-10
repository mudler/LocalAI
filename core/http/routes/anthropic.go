package routes

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/anthropic"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

func RegisterAnthropicRoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	application *application.Application) {

	// Anthropic Messages API endpoint
	messagesHandler := anthropic.MessagesEndpoint(
		application.ModelConfigLoader(),
		application.ModelLoader(),
		application.TemplatesEvaluator(),
		application.ApplicationConfig(),
	)

	messagesMiddleware := []echo.MiddlewareFunc{
		middleware.TraceMiddleware(application),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.AnthropicRequest) }),
		setAnthropicRequestContext(application.ApplicationConfig()),
	}

	// Main Anthropic endpoint
	app.POST("/v1/messages", messagesHandler, messagesMiddleware...)

	// Also support without version prefix for compatibility
	app.POST("/messages", messagesHandler, messagesMiddleware...)
}

// setAnthropicRequestContext sets up the context and cancel function for Anthropic requests
func setAnthropicRequestContext(appConfig *config.ApplicationConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.AnthropicRequest)
			if !ok || input.Model == "" {
				return echo.NewHTTPError(http.StatusBadRequest, "model is required")
			}

			cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			if !ok || cfg == nil {
				return echo.NewHTTPError(http.StatusBadRequest, "model configuration not found")
			}

			// Extract or generate the correlation ID
			// Anthropic uses x-request-id header
			correlationID := c.Request().Header.Get("x-request-id")
			if correlationID == "" {
				correlationID = uuid.New().String()
			}
			c.Response().Header().Set("x-request-id", correlationID)

			// Set up context with cancellation
			reqCtx := c.Request().Context()
			c1, cancel := context.WithCancel(appConfig.Context)

			// Cancel when request context is cancelled (client disconnects)
			go func() {
				select {
				case <-reqCtx.Done():
					cancel()
				case <-c1.Done():
					// Already cancelled
				}
			}()

			// Add the correlation ID to the new context
			ctxWithCorrelationID := context.WithValue(c1, middleware.CorrelationIDKey, correlationID)

			input.Context = ctxWithCorrelationID
			input.Cancel = cancel

			if cfg.Model == "" {
				xlog.Debug("replacing empty cfg.Model with input value", "input.Model", input.Model)
				cfg.Model = input.Model
			}

			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

			// Log the Anthropic API version if provided
			anthropicVersion := c.Request().Header.Get("anthropic-version")
			if anthropicVersion != "" {
				xlog.Debug("Anthropic API version", "version", anthropicVersion)
			}

			// Validate max_tokens is provided
			if input.MaxTokens <= 0 {
				return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("max_tokens is required and must be greater than 0"))
			}

			return next(c)
		}
	}
}
