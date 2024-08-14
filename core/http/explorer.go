package http

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/favicon"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http/routes"
)

func Explorer(db *explorer.Database) *fiber.App {

	fiberCfg := fiber.Config{
		Views: renderEngine(),
		// We disable the Fiber startup message as it does not conform to structured logging.
		// We register a startup log line with connection information in the OnListen hook to keep things user friendly though
		DisableStartupMessage: false,
		// Override default error handler
	}

	app := fiber.New(fiberCfg)

	routes.RegisterExplorerRoutes(app, db)

	httpFS := http.FS(embedDirStatic)

	app.Use(favicon.New(favicon.Config{
		URL:        "/favicon.ico",
		FileSystem: httpFS,
		File:       "static/favicon.ico",
	}))

	app.Use("/static", filesystem.New(filesystem.Config{
		Root:       httpFS,
		PathPrefix: "static",
		Browse:     true,
	}))

	// Define a custom 404 handler
	// Note: keep this at the bottom!
	app.Use(notFoundHandler)

	return app
}
