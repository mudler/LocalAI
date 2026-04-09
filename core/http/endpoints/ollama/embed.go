package ollama

import (
	"fmt"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// EmbedEndpoint handles Ollama-compatible /api/embed and /api/embeddings requests
func EmbedEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OllamaEmbedRequest)
		if !ok || input.Model == "" {
			return ollamaError(c, 400, "model is required")
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return ollamaError(c, 400, "model configuration not found")
		}

		startTime := time.Now()
		inputStrings := input.GetInputStrings()
		if len(inputStrings) == 0 {
			return ollamaError(c, 400, "input is required")
		}

		var allEmbeddings [][]float32
		promptEvalCount := 0

		for _, s := range inputStrings {
			embedFn, err := backend.ModelEmbedding(s, []int{}, ml, *cfg, appConfig)
			if err != nil {
				xlog.Error("Ollama embed failed", "error", err)
				return ollamaError(c, 500, fmt.Sprintf("embedding failed: %v", err))
			}

			embeddings, err := embedFn()
			if err != nil {
				xlog.Error("Ollama embed computation failed", "error", err)
				return ollamaError(c, 500, fmt.Sprintf("embedding computation failed: %v", err))
			}

			allEmbeddings = append(allEmbeddings, embeddings)
			// Rough token count estimate
			promptEvalCount += len(s) / 4
		}

		totalDuration := time.Since(startTime)

		resp := schema.OllamaEmbedResponse{
			Model:           input.Model,
			Embeddings:      allEmbeddings,
			TotalDuration:   totalDuration.Nanoseconds(),
			PromptEvalCount: promptEvalCount,
		}

		return c.JSON(200, resp)
	}
}
