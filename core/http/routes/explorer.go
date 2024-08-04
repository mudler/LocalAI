package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/http/endpoints/explorer"
)

func RegisterExplorerRoutes(app *fiber.App) {
	app.Get("/", explorer.Dashboard())
}
