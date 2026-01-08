package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/jina"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterJINARoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {

	// POST endpoint to mimic the reranking
	rerankHandler := jina.JINARerankEndpoint(cl, ml, appConfig)
	app.POST("/v1/rerank",
		rerankHandler,
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_RERANK)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.JINARerankRequest) }))
}
