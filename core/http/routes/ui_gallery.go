package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

func registerGalleryRoutes(app *echo.Echo, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, galleryService *galleryop.GalleryService, opcache *galleryop.OpCache) {
	// Gallery routes are now handled by the React SPA at /app/browse
	// This function is kept for backward compatibility but no longer registers routes
	// (routes are registered directly in RegisterUIRoutes)
}
