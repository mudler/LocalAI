package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/endpoints/openresponses"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/piiadapter"
	"github.com/mudler/xlog"
)

func RegisterOpenResponsesRoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	application *application.Application) {

	// Agent control for distributed MCP tool routing (nil when not in distributed mode)
	var agentControl mcpTools.AgentControl
	if d := application.Distributed(); d != nil {
		agentControl = d.AgentControl

		// Replicate response metadata across frontend replicas and subscribe to
		// delegated cancels. Without this a GET, a previous_response_id lookup or
		// a cancel that the load balancer sends to a replica other than the
		// creator 404s, and the cancel never reaches the CancelFunc (#10993).
		// Standalone deployments skip this entirely and stay process-local.
		if err := openresponses.GetGlobalStore().EnableDistributed(
			application.ApplicationConfig().Context, d.Nats, application.InstanceID()); err != nil {
			xlog.Error("Failed to enable cross-replica Open Responses store", "error", err)
		}
	}

	// Open Responses API endpoint
	responsesHandler := openresponses.ResponsesEndpoint(
		application.ModelConfigLoader(),
		application.ModelLoader(),
		application.TemplatesEvaluator(),
		application.ApplicationConfig(),
		agentControl,
	)

	responsesMiddleware := openResponsesMiddleware(re, application)

	// Main Open Responses endpoint
	app.POST("/v1/responses", responsesHandler, responsesMiddleware...)

	// Also support without version prefix for compatibility
	app.POST("/responses", responsesHandler, responsesMiddleware...)

	// WebSocket mode for Responses API
	wsHandler := openresponses.WebSocketEndpoint(application)
	app.GET("/v1/responses", wsHandler, middleware.UsageMiddleware(application.StatsRecorder(), application.FallbackUser()), middleware.TraceMiddleware(application))
	app.GET("/responses", wsHandler, middleware.UsageMiddleware(application.StatsRecorder(), application.FallbackUser()), middleware.TraceMiddleware(application))

	// GET /responses/:id - Retrieve a response (for polling background requests)
	getResponseHandler := openresponses.GetResponseEndpoint()
	app.GET("/v1/responses/:id", getResponseHandler, middleware.TraceMiddleware(application))
	app.GET("/responses/:id", getResponseHandler, middleware.TraceMiddleware(application))

	// POST /responses/:id/cancel - Cancel a background response
	cancelResponseHandler := openresponses.CancelResponseEndpoint()
	app.POST("/v1/responses/:id/cancel", cancelResponseHandler, middleware.TraceMiddleware(application))
	app.POST("/responses/:id/cancel", cancelResponseHandler, middleware.TraceMiddleware(application))
}

// openResponsesMiddleware is the middleware chain of POST /v1/responses and
// POST /responses. It is a separate function so tests can drive the real
// chain in front of a stub handler.
func openResponsesMiddleware(re *middleware.RequestExtractor, application *application.Application) []echo.MiddlewareFunc {
	return []echo.MiddlewareFunc{
		middleware.UsageMiddleware(application.StatsRecorder(), application.FallbackUser()),
		middleware.TraceMiddleware(application),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenResponsesRequest) }),
		setOpenResponsesRequestContext(re),
		// PII filtering runs once the model config is resolved and merged
		// (it gates on the served model's pii block), and before the handler
		// turns the input items into prompt messages. Without it, content
		// that a model's PII policy blocks or masks on /v1/chat/completions
		// would reach the same model unfiltered through this endpoint.
		pii.RequestMiddleware(application.PIIRedactor(), application.PIIEvents(), piiadapter.OpenResponses(), application.FallbackUser(), pii.WithNERResolver(application.PIINERResolver()), pii.WithPolicyResolver(application.PIIPolicyResolver())),
	}
}

// setOpenResponsesRequestContext sets up the context and cancel function for Open Responses requests
func setOpenResponsesRequestContext(re *middleware.RequestExtractor) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if err := re.SetOpenResponsesRequest(c); err != nil {
				return err
			}
			return next(c)
		}
	}
}
