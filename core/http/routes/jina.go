package routes

import (
	"github.com/mudler/LocalAI/core"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/jina"
	"github.com/mudler/LocalAI/core/http/middleware"

	"github.com/gofiber/fiber/v2"
)

func RegisterJINARoutes(app *fiber.App, requestExtractor *middleware.RequestExtractor, application *core.Application) {

	// POST endpoint to mimic the reranking
	app.Post("/v1/rerank", requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_RERANK)),
		jina.JINARerankEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	)
}
