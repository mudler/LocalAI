package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
)

func registerGalleryRoutes(app *fiber.App, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) {

	// Show the Models page (all models are loaded client-side via Alpine.js)
	app.Get("/browse", func(c *fiber.Ctx) error {
		summary := fiber.Map{
			"Title":        "LocalAI - Models",
			"BaseURL":      utils.BaseURL(c),
			"Version":      internal.PrintableVersion(),
			"Repositories": appConfig.Galleries,
		}

		// Render index - models are now loaded via Alpine.js from /api/models
		return c.Render("views/models", summary)
	})

	// Note: /browse/search/models was removed - search is now handled client-side with Alpine.js

	// Note: Old HTMX routes for install/delete/config were removed
	// These are now handled by JSON API routes in ui_api.go:
	// - /api/models/install/:id
	// - /api/models/delete/:id
	// - /api/models/config/:id

	// Note: /browse/job/progress/:uid and /browse/job/:uid were removed
	// Progress tracking is now handled via JSON API at /api/models/job/:uid
}
