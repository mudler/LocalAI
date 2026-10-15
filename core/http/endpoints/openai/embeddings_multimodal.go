// SPDX-License-Identifier: MIT
package openai

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// statusClientClosed is the conventional status for a request the client
// abandoned. Nobody reads it, but it keeps cancellation out of the 5xx counters.
const statusClientClosed = 499

// Message prefixes a backend uses to refine a gRPC status code. The message
// text itself never reaches the client.
const (
	backendUnsupportedMediaFormat = "unsupported_media_format"
	backendLimitExceeded          = "limit_exceeded"
)

// grpcStatus returns the gRPC status carried by err, possibly wrapped. The
// status package reports a wrapped error's whole text as the message, so the
// status error is found with errors.As first.
func grpcStatus(err error) (*status.Status, bool) {
	var carrier interface{ GRPCStatus() *status.Status }
	if errors.As(err, &carrier) {
		if st := carrier.GRPCStatus(); st != nil {
			return st, true
		}
	}
	return nil, false
}

// hasReason reports whether msg starts with reason and the reason ends there:
// at the end of the message, or before ':' or a space. A longer word that only
// starts with the reason is a different reason.
func hasReason(msg, reason string) bool {
	rest, ok := strings.CutPrefix(msg, reason)
	return ok && (rest == "" || rest[0] == ':' || rest[0] == ' ')
}

// embeddingFailure classifies an error without ever echoing user input, URLs
// or backend text. Only fixed codes reach the response.
func embeddingFailure(c echo.Context, err error) error {
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	// Capacity, cooldown and loading errors are answers to retry. The
	// application error handler maps them to 429/503 with Retry-After, exactly
	// as for the legacy path, so they leave here unchanged.
	var admission *backend.BackendAdmissionError
	var cooldown *model.ModelLoadCooldownError
	var loading *nodes.ModelLoadingError
	if errors.As(err, &admission) || errors.As(err, &cooldown) || errors.As(err, &loading) || errors.Is(err, nodes.ErrNoAvailableNodes) {
		return err
	}
	var media *embeddingmedia.Error
	if errors.As(err, &media) {
		return echo.NewHTTPError(media.Status, media.Code)
	}
	if backend.IsEmbeddingPoolingCompatibilityError(err) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	var typed *backend.MultimodalEmbeddingError
	if errors.As(err, &typed) {
		return echo.NewHTTPError(typed.StatusCode(), typed.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) || c.Request().Context().Err() == context.DeadlineExceeded {
		return echo.NewHTTPError(http.StatusGatewayTimeout, "embedding_deadline_exceeded")
	}
	// 499 only when this client actually went away. A cancellation that
	// originates in the backend or the session is a backend failure.
	clientGone := c.Request().Context().Err() != nil
	if errors.Is(err, context.Canceled) && clientGone {
		return echo.NewHTTPError(statusClientClosed, "request_canceled")
	}
	if st, ok := grpcStatus(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			if hasReason(st.Message(), backendUnsupportedMediaFormat) {
				return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported_media_format")
			}
			return echo.NewHTTPError(http.StatusBadRequest, "invalid_embedding_input")
		case codes.ResourceExhausted:
			if hasReason(st.Message(), backendLimitExceeded) {
				return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "embedding_resource_limit")
			}
			return echo.NewHTTPError(http.StatusTooManyRequests, "embedding_backend_busy")
		case codes.OutOfRange:
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "embedding_resource_limit")
		case codes.FailedPrecondition:
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "embedding_unsupported_modality")
		case codes.Unimplemented:
			return echo.NewHTTPError(http.StatusNotImplemented, "multimodal_embedding_unavailable")
		case codes.DeadlineExceeded:
			return echo.NewHTTPError(http.StatusGatewayTimeout, "embedding_deadline_exceeded")
		}
	}
	return echo.NewHTTPError(http.StatusBadGateway, "embedding_backend_failure")
}

// structuredParts builds the ordered typed parts of one item. Media bytes are
// attached by the caller; before anything is fetched it also serves as the
// capability skeleton used to check the loaded model.
func structuredParts(item schema.EmbeddingInputItem) []*pb.EmbeddingPart {
	if item.Text != nil || len(item.Content) == 0 {
		s := ""
		if item.Text != nil {
			s = *item.Text
		}
		return []*pb.EmbeddingPart{{Part: &pb.EmbeddingPart_Text{Text: s}}}
	}
	parts := make([]*pb.EmbeddingPart, 0, len(item.Content))
	for _, p := range item.Content {
		switch p.Type {
		case "text":
			parts = append(parts, &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Text{Text: p.Text}})
		default:
			parts = append(parts, &pb.EmbeddingPart{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: mediaKind(p.Type), Format: p.Source.Format}}})
		}
	}
	return parts
}

func mediaKind(partType string) pb.EmbeddingMedia_Kind {
	switch partType {
	case "image_url":
		return pb.EmbeddingMedia_IMAGE
	case "input_audio":
		return pb.EmbeddingMedia_AUDIO
	case "input_video", "video_url":
		return pb.EmbeddingMedia_VIDEO
	}
	return pb.EmbeddingMedia_UNSPECIFIED
}

func sourceKind(partType string) string {
	switch partType {
	case "image_url":
		return embeddingmedia.KindImage
	case "input_audio":
		return embeddingmedia.KindAudio
	}
	return embeddingmedia.KindVideo
}

func toProtoBudget(b embeddingmedia.Budget) *pb.EmbeddingBudget {
	return &pb.EmbeddingBudget{
		RemainingFrames:                     b.Frames,
		RemainingPixels:                     b.Pixels,
		RemainingAudioAllocationBytes:       b.AudioBytes,
		RemainingSourceDurationMilliseconds: b.DurationMilliseconds,
		RemainingTemporaryBytes:             b.TemporaryBytes,
		RemainingPreparationMilliseconds:    b.PreparationMilliseconds,
		RemainingCompressedBytes:            b.MediaBytes,
		RemainingTextBytes:                  b.TextBytes,
		RemainingSourceParts:                b.SourceParts,
	}
}

// fromProtoConsumption never trusts the backend: the caller compares the
// result with the budget it supplied before any subtraction.
func fromProtoConsumption(c *pb.EmbeddingConsumption) embeddingmedia.Consumption {
	return embeddingmedia.Consumption{
		Frames:                  c.GetFrames(),
		Pixels:                  c.GetPixels(),
		AudioBytes:              c.GetAudioAllocationBytes(),
		DurationMilliseconds:    c.GetSourceDurationMilliseconds(),
		TemporaryBytes:          c.GetTemporaryBytes(),
		PreparationMilliseconds: c.GetPreparationMilliseconds(),
		MediaBytes:              c.GetCompressedBytes(),
		TextBytes:               c.GetTextBytes(),
		SourceParts:             c.GetSourceParts(),
	}
}

func hasMedia(item schema.EmbeddingInputItem) bool {
	for _, p := range item.Content {
		if p.Type != "text" {
			return true
		}
	}
	return false
}

func exceeds(c embeddingmedia.Consumption, remaining embeddingmedia.Budget) bool {
	return c.Frames > remaining.Frames || c.Pixels > remaining.Pixels || c.AudioBytes > remaining.AudioBytes ||
		c.DurationMilliseconds > remaining.DurationMilliseconds || c.TemporaryBytes > remaining.TemporaryBytes ||
		c.PreparationMilliseconds > remaining.PreparationMilliseconds || c.MediaBytes > remaining.MediaBytes ||
		c.TextBytes > remaining.TextBytes || c.SourceParts > remaining.SourceParts
}

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
		return nil, embeddingFailure(c, err)
	}

	session, err := backend.OpenMultimodalEmbeddingSession(ctx, ml, *cfg, appConfig)
	if err != nil {
		return nil, embeddingFailure(c, err)
	}
	defer session.Close()

	// Capability check precedes every fetch and every slot.
	var skeleton []*pb.EmbeddingPart
	for _, it := range items {
		skeleton = append(skeleton, structuredParts(it)...)
	}
	if err := backend.CheckEmbeddingModalities(session.Metadata(), skeleton); err != nil {
		return nil, embeddingFailure(c, err)
	}

	release, err := opts.limiter.TryAcquire()
	if err != nil {
		return nil, embeddingFailure(c, err)
	}
	defer release()

	budget := embeddingmedia.NewBudget()
	vectors := make([][]float32, 0, len(items))
	for _, item := range items {
		vec, err := embedStructuredItem(c, ctx, session, item, &budget, *cfg, opts)
		if err != nil {
			return nil, embeddingFailure(c, err)
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
	var known embeddingmedia.Consumption
	if err := work.Consume(embeddingmedia.Consumption{Items: 1}); err != nil {
		return nil, err
	}
	parts := structuredParts(item)
	prepCtx, cancel := context.WithTimeout(ctx, time.Duration(budget.PreparationMilliseconds)*time.Millisecond)
	defer cancel()
	started := time.Now()
	if item.Text != nil || len(item.Content) == 0 {
		n := uint64(len(parts[0].GetText()))
		if err := work.Consume(embeddingmedia.Consumption{TextBytes: n, SourceParts: 1}); err != nil {
			return nil, err
		}
		known.TextBytes, known.SourceParts = n, 1
	} else {
		for i, p := range item.Content {
			if p.Type == "text" {
				n := uint64(len(p.Text))
				if err := work.Consume(embeddingmedia.Consumption{TextBytes: n, SourceParts: 1}); err != nil {
					return nil, err
				}
				known.TextBytes += n
				known.SourceParts++
				continue
			}
			if err := work.Consume(embeddingmedia.Consumption{SourceParts: 1}); err != nil {
				return nil, err
			}
			known.SourceParts++
			// Sequential on purpose: the budget is not synchronized.
			data, err := opts.acquirer.Acquire(prepCtx, embeddingmedia.Source{
				Data: p.Source.Data, URL: p.Source.URL, Format: p.Source.Format, Kind: sourceKind(p.Type),
			}, &work)
			if err != nil {
				return nil, err
			}
			parts[i].GetMedia().Data = data
			known.MediaBytes += uint64(len(data))
		}
	}
	// The acquirer charges what it fetched. Check Go's own count too, so an
	// acquirer that does not charge cannot exceed the request-wide bound.
	if known.MediaBytes > budget.MediaBytes {
		return nil, &embeddingmedia.Error{Status: http.StatusRequestEntityTooLarge, Code: "embedding_resource_limit"}
	}
	elapsed := uint64((time.Since(started) + time.Millisecond - 1) / time.Millisecond)
	if elapsed > budget.PreparationMilliseconds {
		return nil, &embeddingmedia.Error{Status: http.StatusGatewayTimeout, Code: "embedding_preparation_timeout"}
	}
	known.PreparationMilliseconds = elapsed

	supplied := *budget
	supplied.PreparationMilliseconds -= elapsed
	request := &pb.MultimodalEmbeddingRequest{Parts: parts, Budget: toProtoBudget(supplied)}
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
	if res.GetConsumption() == nil && hasMedia(item) {
		return nil, &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"}
	}
	consumed := fromProtoConsumption(res.GetConsumption())
	if exceeds(consumed, supplied) {
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
