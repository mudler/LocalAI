package routes

import (
	"github.com/gofiber/fiber/v2"
	coreExplorer "github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http/endpoints/explorer"
)

func RegisterExplorerRoutes(app *fiber.App, db *coreExplorer.Database) {
	app.Get("/", explorer.Dashboard())
	app.Post("/network/add", explorer.AddNetwork(db))
	app.Get("/networks", explorer.ShowNetworks(db))
}
