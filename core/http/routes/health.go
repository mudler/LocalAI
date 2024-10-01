package routes

import "github.com/gofiber/fiber/v2"

func HealthRoutes(app *fiber.App) {
	// Service health checks
	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	app.Get("/healthz", ok)
	app.Get("/readyz", ok)
}
