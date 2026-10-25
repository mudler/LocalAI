// SPDX-License-Identifier: MIT
package openai

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/multimodal"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"google.golang.org/protobuf/proto"
)

// embedStructured runs one structured batch. All vectors are buffered and
// returned together; any failure discards them, so no partial result escapes.
func embedStructured(c echo.Context, input *schema.OpenAIRequest, cfg *config.ModelConfig, ml *model.ModelLoader, appConfig *config.ApplicationConfig, opts embeddingsOptions) ([][]float32, error) {
	ctx := input.Context
	if ctx == nil {
		ctx = c.Request().Context()
	}
	// The PII middleware rewrites input.Input after the request was parsed into
	// EmbeddingInput. Read the text back from input.Input so what reaches the
	// backend is the redacted text, not the text the client sent.
	items, err := currentEmbeddingItems(input)
	if err != nil {
		return nil, multimodal.Failure(c, err)
	}

	session, err := backend.OpenMultimodalEmbeddingSession(ctx, ml, *cfg, appConfig)
	if err != nil {
		return nil, multimodal.Failure(c, err)
	}
	defer func() { _ = session.Close() }() // best-effort release; the response is already decided

	// Capability check precedes every fetch and every slot.
	var skeleton []*pb.EmbeddingPart
	for _, it := range items {
		skeleton = append(skeleton, multimodal.StructuredParts(it)...)
	}
	if err := backend.CheckEmbeddingModalities(session.Metadata(), skeleton); err != nil {
		return nil, multimodal.Failure(c, err)
	}

	release, err := opts.limiter.TryAcquire()
	if err != nil {
		return nil, multimodal.Failure(c, err)
	}
	defer release()

	budget := embeddingmedia.NewBudget()
	vectors := make([][]float32, 0, len(items))
	for _, item := range items {
		vec, err := embedStructuredItem(c, ctx, session, item, &budget, *cfg, opts)
		if err != nil {
			return nil, multimodal.Failure(c, err)
		}
		vectors = append(vectors, vec)
	}
	return vectors, nil
}

func embedStructuredItem(c echo.Context, ctx context.Context, session *backend.MultimodalEmbeddingSession, item schema.EmbeddingInputItem, budget *embeddingmedia.Budget, cfg config.ModelConfig, opts embeddingsOptions) ([]float32, error) {
	// The scratch budget lets the acquirer enforce cumulative limits inside the
	// item. What the backend may spend is the live budget, minus preparation
	// time already used here.
	work := *budget
	if err := work.Consume(embeddingmedia.Consumption{Items: 1}); err != nil {
		return nil, err
	}
	prepCtx, cancel := context.WithTimeout(ctx, multimodal.PreparationTimeout(budget.PreparationMilliseconds))
	defer cancel()
	started := time.Now()
	parts, known, err := multimodal.PrepareItem(prepCtx, opts.acquirer, item, &work)
	if err != nil {
		return nil, err
	}
	// The acquirer charges what it fetched. Check Go's own count too, so an
	// acquirer that does not charge cannot exceed the request-wide bound.
	if known.MediaBytes > budget.MediaBytes {
		return nil, &embeddingmedia.Error{Status: http.StatusRequestEntityTooLarge, Code: "embedding_resource_limit"}
	}
	elapsed := multimodal.ElapsedMilliseconds(started)
	if elapsed > budget.PreparationMilliseconds {
		return nil, &embeddingmedia.Error{Status: http.StatusGatewayTimeout, Code: "embedding_preparation_timeout"}
	}
	known.PreparationMilliseconds = elapsed

	supplied := *budget
	supplied.PreparationMilliseconds -= elapsed
	request := &pb.MultimodalEmbeddingRequest{Parts: parts, Budget: multimodal.ToProtoBudget(supplied)}
	if proto.Size(request) > backend.MultimodalMaxRPCBytes {
		return nil, &embeddingmedia.Error{Status: http.StatusRequestEntityTooLarge, Code: "embedding_resource_limit"}
	}
	res, err := session.Embed(ctx, request)
	if err != nil {
		return nil, err
	}
	// A response that wraps no consumption cannot be charged. For an item with
	// media that would let a backend bypass the frame, pixel, audio, duration
	// and temporary-byte budgets, so it is a corrupt result. Text-only items
	// consume none of those and may omit it.
	if res.GetConsumption() == nil && multimodal.HasMedia(item) {
		return nil, &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"}
	}
	consumed := multimodal.FromProtoConsumption(res.GetConsumption())
	if multimodal.Exceeds(consumed, supplied) {
		return nil, &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"}
	}
	vec, err := backend.FinishMultimodalEmbedding(res, cfg)
	if err != nil {
		return nil, err
	}
	// Go counted these exactly; a backend cannot report less than it received.
	consumed.MediaBytes = max(consumed.MediaBytes, known.MediaBytes)
	consumed.TextBytes = max(consumed.TextBytes, known.TextBytes)
	consumed.SourceParts = max(consumed.SourceParts, known.SourceParts)
	consumed.PreparationMilliseconds += known.PreparationMilliseconds
	consumed.Items = 1
	if err := budget.Consume(consumed); err != nil {
		return nil, &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"}
	}
	return vec, nil
}

// currentEmbeddingItems returns the parsed items with their text taken from the
// request's current input value, which the PII middleware may have rewritten.
// Media sources are not text and are kept as parsed. A value whose shape no
// longer matches the parsed items fails closed instead of sending the text the
// client originally supplied.
func currentEmbeddingItems(input *schema.OpenAIRequest) ([]schema.EmbeddingInputItem, error) {
	var entries []any
	switch v := input.Input.(type) {
	case []any:
		entries = v
	case map[string]any:
		entries = []any{v}
	default:
		entries = nil
	}
	if len(entries) != len(input.EmbeddingInput) {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "invalid_embedding_input")
	}
	out := make([]schema.EmbeddingInputItem, len(input.EmbeddingInput))
	for i, item := range input.EmbeddingInput {
		cur := schema.EmbeddingInputItem{}
		switch e := entries[i].(type) {
		case string:
			if item.Text == nil {
				return nil, echo.NewHTTPError(http.StatusInternalServerError, "invalid_embedding_input")
			}
			cur.Text = &e
		case map[string]any:
			parts, _ := e["content"].([]any)
			if item.Text != nil || len(parts) != len(item.Content) {
				return nil, echo.NewHTTPError(http.StatusInternalServerError, "invalid_embedding_input")
			}
			cur.Content = append([]schema.EmbeddingContentPart(nil), item.Content...)
			for j, raw := range parts {
				if cur.Content[j].Type != "text" {
					continue
				}
				m, ok := raw.(map[string]any)
				text, isString := "", false
				if ok {
					text, isString = m["text"].(string)
				}
				if !isString {
					return nil, echo.NewHTTPError(http.StatusInternalServerError, "invalid_embedding_input")
				}
				cur.Content[j].Text = text
			}
		default:
			return nil, echo.NewHTTPError(http.StatusInternalServerError, "invalid_embedding_input")
		}
		out[i] = cur
	}
	return out, nil
}
