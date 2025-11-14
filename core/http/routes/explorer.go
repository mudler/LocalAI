package routes

import (
	"github.com/labstack/echo/v4"
	coreExplorer "github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http/endpoints/explorer"
)

func RegisterExplorerRoutes(app *echo.Echo, db *coreExplorer.Database) {
	app.GET("/", explorer.Dashboard())
	app.POST("/network/add", explorer.AddNetwork(db))
	app.GET("/networks", explorer.ShowNetworks(db))
}
