package jina

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
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
func JINARerankEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.JINARerankRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		log.Debug().Str("model", input.Model).Msg("JINA Rerank Request received")
		var requestTopN int32
		docs := int32(len(input.Documents))
		if input.TopN == nil { // omit top_n to get all
			requestTopN = docs
		} else {
			requestTopN = int32(*input.TopN)
			if requestTopN < 1 {
				return c.JSON(http.StatusUnprocessableEntity, "top_n - should be greater than or equal to 1")
			}
			if requestTopN > docs { // make it more obvious for backends
				requestTopN = docs
			}
		}
		request := &proto.RerankRequest{
			Query:     input.Query,
			TopN:      requestTopN,
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

		return c.JSON(http.StatusOK, response)
	}
}
