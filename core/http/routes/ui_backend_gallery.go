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

	// Note: /browse/search/backends was removed - search is now handled client-side with Alpine.js

	// Note: Old HTMX routes for backend install/delete were removed
	// These are now handled by JSON API routes in ui_api.go:
	// - /api/backends/install/:id
	// - /api/backends/delete/:id

	// Note: /browse/backend/job/progress/:uid and /browse/backend/job/:uid were removed
	// Progress tracking is now handled via JSON API at /api/backends/job/:uid
}
