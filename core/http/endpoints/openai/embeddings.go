package openai

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/mudler/xlog"
)

// floatsToBase64 packs a float32 slice as little-endian bytes and returns a base64 string.
// This matches the OpenAI API encoding_format=base64 contract expected by the Node.js SDK.
func floatsToBase64(floats []float32) string {
	buf := make([]byte, len(floats)*4)
	for i, f := range floats {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// embeddingItem builds a schema.Item for an embedding, encoding as base64 when requested.
// The OpenAI Node.js SDK (v4+) sends encoding_format=base64 by default and expects a base64
// string in the response; returning a float array causes Buffer.from(array,'base64') to
// interpret each float as a single byte, yielding dims/4 values in Qdrant.
func embeddingItem(embeddings []float32, index int, encodingFormat string) schema.Item {
	if encodingFormat == "base64" {
		return schema.Item{EmbeddingBase64: floatsToBase64(embeddings), Index: index, Object: "embedding"}
	}
	return schema.Item{Embedding: embeddings, Index: index, Object: "embedding"}
}

// EmbeddingsEndpoint is the OpenAI Embeddings API endpoint https://platform.openai.com/docs/api-reference/embeddings
// @Summary Get a vector representation of a given input that can be easily consumed by machine learning models and algorithms.
// @Tags embeddings
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/embeddings [post]
func EmbeddingsEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("Parameter Config", "config", config)
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
			items = append(items, embeddingItem(embeddings, i, input.EncodingFormat))
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
			items = append(items, embeddingItem(embeddings, i, input.EncodingFormat))
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
		xlog.Debug("Response", "response", string(jsonResult))

		// Return the prediction in the response body
		return c.JSON(200, resp)
	}
}
