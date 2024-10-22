package jina

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"

	"github.com/gofiber/fiber/v2"
	fiberContext "github.com/mudler/LocalAI/core/http/ctx"
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
func JINARerankEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		req := new(schema.JINARerankRequest)
		if err := c.BodyParser(req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Cannot parse JSON",
			})
		}

		input := new(schema.TTSRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.Model, false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}

		cfg, err := cl.LoadBackendConfigFileByName(modelFile, appConfig.ModelPath,
			config.LoadOptionDebug(appConfig.Debug),
			config.LoadOptionThreads(appConfig.Threads),
			config.LoadOptionContextSize(appConfig.ContextSize),
			config.LoadOptionF16(appConfig.F16),
		)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		} else {
			modelFile = cfg.Model
		}

		log.Debug().Msgf("Request for model: %s", modelFile)

		if input.Backend != "" {
			cfg.Backend = input.Backend
		}

		request := &proto.RerankRequest{
			Query:     req.Query,
			TopN:      int32(req.TopN),
			Documents: req.Documents,
		}

		results, err := backend.Rerank(modelFile, request, ml, appConfig, *cfg)
		if err != nil {
			return err
		}

		response := &schema.JINARerankResponse{
			Model: req.Model,
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
