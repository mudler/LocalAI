package localai

import (
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/gofiber/fiber/v2"
)

func WelcomeEndpoint(appConfig *config.ApplicationConfig,
	models []string, backendConfigs []config.BackendConfig) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		summary := fiber.Map{
			"Title":             "LocalAI API - " + internal.PrintableVersion(),
			"Version":           internal.PrintableVersion(),
			"Models":            models,
			"ModelsConfig":      backendConfigs,
			"ApplicationConfig": appConfig,
		}

		if string(c.Context().Request.Header.ContentType()) == "application/json" || len(c.Accepts("html")) == 0 {
			// The client expects a JSON response
			return c.Status(fiber.StatusOK).JSON(summary)
		} else {
			// Render index
			return c.Render("views/index", summary)
		}
	}
}
