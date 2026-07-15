package openai

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/templates"
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
// LocalAI extensions: a chat conversation can be embedded by sending
// `messages` (mutually exclusive with `input`; one conversation per request,
// one data item in the response), and `pooling`/`pooling_half_life_tokens`
// select a Go-side pooling scheme over the backend's per-token vectors.
// @Summary Get a vector representation of a given input that can be easily consumed by machine learning models and algorithms.
// @Tags embeddings
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/embeddings [post]
func EmbeddingsEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		modelConfig, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || modelConfig == nil {
			return echo.ErrBadRequest
		}

		// The middleware merged any per-request pooling override onto the
		// per-request config copy; reject bad values before touching the
		// model so the client gets a 400, not a load-time failure.
		if err := config.ValidatePooling(modelConfig.Pooling, modelConfig.PoolingHalfLifeTokens); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		if len(input.Messages) > 0 {
			// One conversation per request: messages[] renders to a single
			// input string, so it cannot be combined with input.
			if len(modelConfig.InputStrings) > 0 || len(modelConfig.InputToken) > 0 {
				return echo.NewHTTPError(http.StatusBadRequest, "input and messages are mutually exclusive: send the conversation via messages, or plain text/tokens via input")
			}
			// Non-text parts were parked in StringImages/StringVideos/
			// StringAudios by the request middleware; only text embeds.
			for _, m := range input.Messages {
				if len(m.StringImages) > 0 || len(m.StringVideos) > 0 || len(m.StringAudios) > 0 {
					xlog.Debug("embeddings: ignoring non-text content parts in messages", "model", modelConfig.Name)
					break
				}
			}
			rendered := evaluator.RenderConversationForEmbedding(*input, input.Messages, modelConfig)
			modelConfig.InputStrings = append(modelConfig.InputStrings, rendered)
		}

		xlog.Debug("Parameter Config", "config", modelConfig)
		items := []schema.Item{}

		for i, s := range modelConfig.InputToken {
			// get the model function to call for the result
			embedFn, err := backend.ModelEmbedding(input.Context, "", s, ml, *modelConfig, appConfig)
			if err != nil {
				return err
			}

			embeddings, err := embedFn()
			if err != nil {
				return err
			}
			items = append(items, embeddingItem(embeddings, i, input.EncodingFormat))
		}

		for i, s := range modelConfig.InputStrings {
			// get the model function to call for the result
			embedFn, err := backend.ModelEmbedding(input.Context, s, []int{}, ml, *modelConfig, appConfig)
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

		// LocalAI's embeddings endpoint does not currently track per-call
		// token counts (the gRPC Embedding RPC returns a vector, not a
		// usage block), so we stamp with zeros. The point of stamping is
		// that the billing pipeline still sees the request and emits the
		// localai_billed_requests_total counter; without this the call
		// would be silently dropped by the unrecorded-counter path. When
		// embeddings learn to report usage, swap the zeros for real counts.
		middleware.StampUsage(c, input.Model, 0, 0)

		// Return the prediction in the response body
		return c.JSON(200, resp)
	}
}
