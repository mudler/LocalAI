package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

func registerBackendGalleryRoutes(app *echo.Echo, appConfig *config.ApplicationConfig, galleryService *galleryop.GalleryService, opcache *galleryop.OpCache) {
	// Backend gallery routes are now handled by the React SPA at /app/backends
	// This function is kept for backward compatibility but no longer registers routes
	// (routes are registered directly in RegisterUIRoutes)
}
