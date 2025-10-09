package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
)

func registerGalleryRoutes(app *fiber.App, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) {

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
}
