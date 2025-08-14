package routes

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/jina"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterJINARoutes(app *fiber.App,
	re *middleware.RequestExtractor,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {

	// POST endpoint to mimic the reranking
	app.Post("/v1/rerank",
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_RERANK)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.JINARerankRequest) }),
		jina.JINARerankEndpoint(cl, ml, appConfig))
}
