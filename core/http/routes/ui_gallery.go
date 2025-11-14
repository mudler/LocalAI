package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
)

func registerGalleryRoutes(app *echo.Echo, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) {

	app.GET("/browse/", func(c echo.Context) error {
		summary := map[string]interface{}{
			"Title":        "LocalAI - Models",
			"BaseURL":      middleware.BaseURL(c),
			"Version":      internal.PrintableVersion(),
			"Repositories": appConfig.Galleries,
		}

		// Render index - models are now loaded via Alpine.js from /api/models
		return c.Render(200, "views/models", summary)
	})
}
