package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// https://platform.openai.com/docs/api-reference/embeddings
func EmbeddingsEndpoint(cl *backend.ConfigLoader, ml *model.ModelLoader, so *datamodel.StartupOptions) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		model, input, err := readInput(c, so, ml, true)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		config, input, err := readConfig(model, input, cl, ml, so.Debug, so.Threads, so.ContextSize, so.F16)
		if err != nil {
			return fmt.Errorf("failed reading parameters from request:%w", err)
		}

		log.Debug().Msgf("Parameter Config: %+v", config)
		items := []datamodel.Item{}

		for i, s := range config.InputToken {
			// get the model function to call for the result
			embedFn, err := backend.ModelEmbedding("", s, ml, *config, so)
			if err != nil {
				return err
			}

			embeddings, err := embedFn()
			if err != nil {
				return err
			}
			items = append(items, datamodel.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		for i, s := range config.InputStrings {
			// get the model function to call for the result
			embedFn, err := backend.ModelEmbedding(s, []int{}, ml, *config, so)
			if err != nil {
				return err
			}

			embeddings, err := embedFn()
			if err != nil {
				return err
			}
			items = append(items, datamodel.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &datamodel.OpenAIResponse{
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
