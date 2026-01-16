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
