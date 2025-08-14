package jina

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// JINARerankEndpoint acts like the Jina reranker endpoint (https://jina.ai/reranker/)
// @Summary Reranks a list of phrases by relevance to a given text query.
// @Param request body schema.JINARerankRequest true "query params"
// @Success 200 {object} schema.JINARerankResponse "Response"
// @Router /v1/rerank [post]
func JINARerankEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.JINARerankRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}

		cfg, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return fiber.ErrBadRequest
		}

		log.Debug().Str("model", input.Model).Msg("JINA Rerank Request received")

		request := &proto.RerankRequest{
			Query:     input.Query,
			TopN:      int32(input.TopN),
			Documents: input.Documents,
		}

		results, err := backend.Rerank(request, ml, appConfig, *cfg)
		if err != nil {
			return err
		}

		response := &schema.JINARerankResponse{
			Model: input.Model,
		}

		for _, r := range results.Results {
			response.Results = append(response.Results, schema.JINADocumentResult{
				Index:          int(r.Index),
				Document:       schema.JINAText{Text: r.Text},
				RelevanceScore: float64(r.RelevanceScore),
			})
		}

		response.Usage.TotalTokens = int(results.Usage.TotalTokens)
		response.Usage.PromptTokens = int(results.Usage.PromptTokens)

		return c.Status(fiber.StatusOK).JSON(response)
	}
}
