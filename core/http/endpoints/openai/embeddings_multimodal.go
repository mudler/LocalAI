// SPDX-License-Identifier: MIT
package openai

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
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

// embeddingFailure classifies an error without ever echoing user input, URLs
// or backend text. Only fixed codes reach the response.
func embeddingFailure(c echo.Context, err error) error {
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
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
	if errors.Is(err, context.Canceled) {
		return echo.NewHTTPError(statusClientClosed, "request_canceled")
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			return echo.NewHTTPError(http.StatusBadRequest, "invalid_embedding_input")
		case codes.OutOfRange, codes.ResourceExhausted:
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "embedding_resource_limit")
		case codes.FailedPrecondition:
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "embedding_unsupported_modality")
		case codes.Unimplemented:
			return echo.NewHTTPError(http.StatusNotImplemented, "multimodal_embedding_unavailable")
		case codes.DeadlineExceeded:
			return echo.NewHTTPError(http.StatusGatewayTimeout, "embedding_deadline_exceeded")
		case codes.Canceled:
			return echo.NewHTTPError(statusClientClosed, "request_canceled")
		}
	}
	return echo.NewHTTPError(http.StatusBadGateway, "embedding_backend_failure")
}

// structuredParts builds the ordered typed parts of one item. Media bytes are
// attached by the caller; a nil acquire builds the capability skeleton used to
// check the loaded model before anything is fetched.
func structuredParts(item schema.EmbeddingInputItem, text *string) []*pb.EmbeddingPart {
	if item.Text != nil || len(item.Content) == 0 {
		s := ""
		if text != nil {
			s = *text
		} else if item.Text != nil {
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
	items := input.EmbeddingInput
	// Redaction middleware rewrites the legacy string view of the input; use it
	// for string items so the redacted text is what reaches the backend.
	var strs []string
	stringItems := 0
	for _, it := range items {
		if it.Text != nil {
			stringItems++
		}
	}
	if len(cfg.InputStrings) == stringItems {
		strs = cfg.InputStrings
	}
	textFor := func(item schema.EmbeddingInputItem, nth int) *string {
		if item.Text != nil && nth < len(strs) {
			return &strs[nth]
		}
		return nil
	}

	session, err := backend.OpenMultimodalEmbeddingSession(ctx, ml, *cfg, appConfig)
	if err != nil {
		return nil, embeddingFailure(c, err)
	}
	defer session.Close()

	// Capability check precedes every fetch and every slot.
	var skeleton []*pb.EmbeddingPart
	for _, it := range items {
		skeleton = append(skeleton, structuredParts(it, nil)...)
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
	nth := 0
	for _, item := range items {
		var text *string
		if item.Text != nil {
			text = textFor(item, nth)
			nth++
		}
		vec, err := embedStructuredItem(c, ctx, session, item, text, &budget, *cfg, opts)
		if err != nil {
			return nil, embeddingFailure(c, err)
		}
		vectors = append(vectors, vec)
	}
	return vectors, nil
}

func embedStructuredItem(c echo.Context, ctx context.Context, session *backend.MultimodalEmbeddingSession, item schema.EmbeddingInputItem, text *string, budget *embeddingmedia.Budget, cfg config.ModelConfig, opts embeddingsOptions) ([]float32, error) {
	// The scratch budget lets the acquirer enforce cumulative limits inside the
	// item. What the backend may spend is the live budget, minus preparation
	// time already used here.
	work := *budget
	var known embeddingmedia.Consumption
	if err := work.Consume(embeddingmedia.Consumption{Items: 1}); err != nil {
		return nil, err
	}
	parts := structuredParts(item, text)
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
