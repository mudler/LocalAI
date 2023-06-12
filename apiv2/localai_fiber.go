package apiv2

import (
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
)

func RegisterNewLocalAIFiberServer(configManager *ConfigManager, loader *model.ModelLoader, app *fiber.App) *LocalAIServer {
	engine := NewLocalAIEngine(loader)
	localAI := LocalAIServer{
		configManager: configManager,
		loader:        loader,
		engine:        &engine,
	}

	v2Group := app.Group("/v2")
	var mw []StrictMiddlewareFunc

	// Use our validation middleware to check all requests against the
	// OpenAPI schema.
	// v2Group.Use(middleware.OapiRequestValidator(swagger))

	// We now register our petStore above as the handler for the interface
	RegisterHandlers(v2Group, NewStrictHandler(&localAI, mw))

	return &localAI
}
