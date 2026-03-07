package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterUIRoutes(app *echo.Echo,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	// SPA routes are handled by the 404 fallback in app.go which serves
	// index.html for any unmatched HTML request, enabling client-side routing.

	app.GET("/api/traces", func(c echo.Context) error {
		return c.JSON(200, middleware.GetTraces())
	})

	app.POST("/api/traces/clear", func(c echo.Context) error {
		middleware.ClearTraces()
		return c.NoContent(204)
	})

	app.GET("/api/backend-traces", func(c echo.Context) error {
		return c.JSON(200, trace.GetBackendTraces())
	})

	app.POST("/api/backend-traces/clear", func(c echo.Context) error {
		trace.ClearBackendTraces()
		return c.NoContent(204)
	})

}
