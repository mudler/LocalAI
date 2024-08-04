package explorer

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/internal"
)

func Dashboard() func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		summary := fiber.Map{
			"Title":   "LocalAI API - " + internal.PrintableVersion(),
			"Version": internal.PrintableVersion(),
		}

		if string(c.Context().Request.Header.ContentType()) == "application/json" || len(c.Accepts("html")) == 0 {
			// The client expects a JSON response
			return c.Status(fiber.StatusOK).JSON(summary)
		} else {
			// Render index
			return c.Render("views/explorer", summary)
		}
	}
}
