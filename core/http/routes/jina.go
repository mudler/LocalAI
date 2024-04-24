package routes

import (
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/http/endpoints/jina"

	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
)

func RegisterJINARoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	auth func(*fiber.Ctx) error) {

	// POST endpoint to mimic the reranking
	app.Post("/v1/rerank", jina.JINARerankEndpoint(cl, ml, appConfig))
}
