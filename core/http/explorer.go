package http

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/http/routes"
)

func Explorer() *fiber.App {

	fiberCfg := fiber.Config{
		Views: renderEngine(),
		// We disable the Fiber startup message as it does not conform to structured logging.
		// We register a startup log line with connection information in the OnListen hook to keep things user friendly though
		DisableStartupMessage: true,
		// Override default error handler
	}

	app := fiber.New(fiberCfg)
	routes.RegisterExplorerRoutes(app)

	return app
}
