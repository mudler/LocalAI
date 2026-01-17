package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openresponses"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
)

func RegisterOpenResponsesRoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	application *application.Application) {

	// Open Responses API endpoint
	responsesHandler := openresponses.ResponsesEndpoint(
		application.ModelConfigLoader(),
		application.ModelLoader(),
		application.TemplatesEvaluator(),
		application.ApplicationConfig(),
	)

	responsesMiddleware := []echo.MiddlewareFunc{
		middleware.TraceMiddleware(application),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenResponsesRequest) }),
		setOpenResponsesRequestContext(re),
	}

	// Main Open Responses endpoint
	app.POST("/v1/responses", responsesHandler, responsesMiddleware...)

	// Also support without version prefix for compatibility
	app.POST("/responses", responsesHandler, responsesMiddleware...)

	// GET /responses/:id - Retrieve a response (for polling background requests)
	getResponseHandler := openresponses.GetResponseEndpoint()
	app.GET("/v1/responses/:id", getResponseHandler, middleware.TraceMiddleware(application))
	app.GET("/responses/:id", getResponseHandler, middleware.TraceMiddleware(application))

	// POST /responses/:id/cancel - Cancel a background response
	cancelResponseHandler := openresponses.CancelResponseEndpoint()
	app.POST("/v1/responses/:id/cancel", cancelResponseHandler, middleware.TraceMiddleware(application))
	app.POST("/responses/:id/cancel", cancelResponseHandler, middleware.TraceMiddleware(application))
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
