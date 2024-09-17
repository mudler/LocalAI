package routes

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/jina"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterJINARoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {

	// POST endpoint to mimic the reranking
	app.Post("/v1/rerank", jina.JINARerankEndpoint(cl, ml, appConfig))
}
