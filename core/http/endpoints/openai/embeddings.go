package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

// EmbeddingsEndpoint is the OpenAI Embeddings API endpoint https://platform.openai.com/docs/api-reference/embeddings
// @Summary Get a vector representation of a given input that can be easily consumed by machine learning models and algorithms.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/embeddings [post]
func EmbeddingsEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		model, input, err := readRequest(c, cl, ml, appConfig, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := mergeRequestWithConfig(model, input, cl, ml, appConfig.Debug, appConfig.Threads, appConfig.ContextSize, appConfig.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)
		items := []schema.Item{}

		for i, s := range config.InputToken {
			// get the model function to call for the result
			embedFn, err := backend.ModelEmbedding("", s, ml, *config, appConfig)
			if err != nil {
				return err
			}

			embeddings, err := embedFn()
			if err != nil {
				return err
			}
			items = append(items, schema.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		for i, s := range config.InputStrings {
			// get the model function to call for the result
			embedFn, err := backend.ModelEmbedding(s, []int{}, ml, *config, appConfig)
			if err != nil {
				return err
			}

			embeddings, err := embedFn()
			if err != nil {
				return err
			}
			items = append(items, schema.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
			Data:    items,
			Object:  "list",
		}

		jsonResult, _ := json.Marshal(resp)
		log.Debug().Msgf("Response: %s", jsonResult)

		// Return the prediction in the response body
		return c.JSON(resp)
	}
}
