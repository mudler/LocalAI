package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
)

func registerBackendGalleryRoutes(app *fiber.App, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) {
	// Show the Backends page (all backends are loaded client-side via Alpine.js)
	app.Get("/browse/backends", func(c *fiber.Ctx) error {
		summary := fiber.Map{
			"Title":        "LocalAI - Backends",
			"BaseURL":      utils.BaseURL(c),
			"Version":      internal.PrintableVersion(),
			"Repositories": appConfig.BackendGalleries,
		}

		// Render index - backends are now loaded via Alpine.js from /api/backends
		return c.Render("views/backends", summary)
	})
}
