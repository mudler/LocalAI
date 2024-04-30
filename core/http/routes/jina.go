package routes

import (
	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/http/endpoints/jina"

	"github.com/gofiber/fiber/v2"
)

func RegisterJINARoutes(app *fiber.App,
	rbs *backend.RerankBackendService,
	fce *ctx.FiberContentExtractor,
	auth func(*fiber.Ctx) error) {

	// POST endpoint to mimic the reranking
	app.Post("/v1/rerank", jina.JINARerankEndpoint(rbs, fce))
}
