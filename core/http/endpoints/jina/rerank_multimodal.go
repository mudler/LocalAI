// SPDX-License-Identifier: MIT
package jina

import (
	"context"
	"math"
	"net/http"
	"strings"
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

type rerankOptions struct {
	acquirer multimodal.MediaAcquirer
	limiter  *embeddingmedia.Limiter
}

// RerankOption customizes JINARerankEndpoint; production uses the defaults.
type RerankOption func(*rerankOptions)

// WithMediaAcquirer replaces the bounded media acquirer (tests use a fake).
func WithMediaAcquirer(a multimodal.MediaAcquirer) RerankOption {
	return func(o *rerankOptions) { o.acquirer = a }
}

// WithPreparationLimiter shares a preparation limiter with other endpoints.
func WithPreparationLimiter(l *embeddingmedia.Limiter) RerankOption {
	return func(o *rerankOptions) { o.limiter = l }
}

func newRerankOptions(opts []RerankOption) rerankOptions {
	o := rerankOptions{}
	for _, f := range opts {
		f(&o)
	}
	if o.acquirer == nil {
		o.acquirer = embeddingmedia.NewAcquirer()
	}
	if o.limiter == nil {
		o.limiter = embeddingmedia.NewPreparationLimiter()
	}
	return o
}

// itemText joins the text parts of an item, with no separators. It is what a
// text-only reranker sees, and what the response shows as the document text:
// never a media location or any media bytes.
func itemText(item schema.EmbeddingInputItem) string {
	if item.Text != nil {
		return *item.Text
	}
	var b strings.Builder
	for _, p := range item.Content {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// typedTexts returns the query text and the document texts of a typed request.
func typedTexts(content *schema.RerankContent) (string, []string) {
	docs := make([]string, len(content.Documents))
	for i, d := range content.Documents {
		docs[i] = itemText(d)
	}
	return itemText(content.Query), docs
}

// rerankTyped scores a request whose query or documents are content objects.
//
// A request without media has nothing to drop, so it takes the text RPC with
// the joined text. A request with media goes through the typed RPC, and it is
// never retried as text: a backend or model that cannot consume the media
// answers 501 or 422 instead of scoring a text-only approximation.
func rerankTyped(c echo.Context, input *schema.JINARerankRequest, cfg *config.ModelConfig, ml *model.ModelLoader, appConfig *config.ApplicationConfig, topN int32, opts rerankOptions) (*pb.RerankResult, []string, error) {
	content := input.Typed
	query, docTexts := typedTexts(content)
	ctx := c.Request().Context()

	if !content.HasMedia() {
		res, err := backend.Rerank(ctx, &pb.RerankRequest{Query: query, TopN: topN, Documents: docTexts}, ml, appConfig, *cfg)
		return res, docTexts, err
	}

	session, err := backend.OpenMultimodalRerankSession(ctx, ml, *cfg, appConfig)
	if err != nil {
		return nil, nil, multimodal.Failure(c, err)
	}
	defer func() { _ = session.Close() }() // best-effort release; the response is already decided

	items := append([]schema.EmbeddingInputItem{content.Query}, content.Documents...)
	// Capability check precedes every fetch and every slot.
	var skeleton []*pb.EmbeddingPart
	for _, it := range items {
		skeleton = append(skeleton, multimodal.StructuredParts(it)...)
	}
	if err := backend.CheckRerankModalities(session.Metadata(), skeleton); err != nil {
		return nil, nil, multimodal.Failure(c, err)
	}

	release, err := opts.limiter.TryAcquire()
	if err != nil {
		return nil, nil, multimodal.Failure(c, err)
	}
	defer release()

	budget := embeddingmedia.NewBudget()
	work := budget
	prepCtx, cancel := context.WithTimeout(ctx, multimodal.PreparationTimeout(budget.PreparationMilliseconds))
	defer cancel()
	started := time.Now()
	var known embeddingmedia.Consumption
	prepared := make([][]*pb.EmbeddingPart, len(items))
	for i, it := range items {
		parts, k, err := multimodal.PrepareItem(prepCtx, opts.acquirer, it, &work)
		if err != nil {
			return nil, nil, multimodal.Failure(c, err)
		}
		prepared[i] = parts
		known.MediaBytes += k.MediaBytes
		known.TextBytes += k.TextBytes
		known.SourceParts += k.SourceParts
	}
	if known.MediaBytes > budget.MediaBytes {
		return nil, nil, multimodal.Failure(c, &embeddingmedia.Error{Status: http.StatusRequestEntityTooLarge, Code: "embedding_resource_limit"})
	}
	elapsed := multimodal.ElapsedMilliseconds(started)
	if elapsed > budget.PreparationMilliseconds {
		return nil, nil, multimodal.Failure(c, &embeddingmedia.Error{Status: http.StatusGatewayTimeout, Code: "embedding_preparation_timeout"})
	}
	known.PreparationMilliseconds = elapsed

	supplied := budget
	supplied.PreparationMilliseconds -= elapsed
	request := &pb.MultimodalRerankRequest{Query: prepared[0], TopN: topN, Budget: multimodal.ToProtoBudget(supplied)}
	for _, parts := range prepared[1:] {
		request.Documents = append(request.Documents, &pb.RerankDocument{Parts: parts})
	}
	if proto.Size(request) > backend.MultimodalMaxRPCBytes {
		return nil, nil, multimodal.Failure(c, &embeddingmedia.Error{Status: http.StatusRequestEntityTooLarge, Code: "embedding_resource_limit"})
	}
	res, err := session.Rerank(ctx, request)
	if err != nil {
		return nil, nil, multimodal.Failure(c, err)
	}
	// Media was sent, so a missing or oversized consumption report cannot be
	// charged and the whole answer is a corrupt result.
	if res.GetConsumption() == nil {
		return nil, nil, multimodal.Failure(c, &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"})
	}
	consumed := multimodal.FromProtoConsumption(res.GetConsumption())
	if multimodal.Exceeds(consumed, supplied) {
		return nil, nil, multimodal.Failure(c, &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"})
	}
	if err := checkRerankResult(res.GetResult(), len(content.Documents), topN); err != nil {
		return nil, nil, multimodal.Failure(c, err)
	}
	consumed.MediaBytes = max(consumed.MediaBytes, known.MediaBytes)
	consumed.TextBytes = max(consumed.TextBytes, known.TextBytes)
	consumed.SourceParts = max(consumed.SourceParts, known.SourceParts)
	consumed.PreparationMilliseconds += known.PreparationMilliseconds
	if err := budget.Consume(consumed); err != nil {
		return nil, nil, multimodal.Failure(c, &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"})
	}
	return res.GetResult(), docTexts, nil
}

// checkRerankResult refuses a result that cannot be an answer to this request:
// more rows than asked for, an index outside the documents or repeated, or a
// score that is not a finite number.
func checkRerankResult(r *pb.RerankResult, documents int, topN int32) error {
	corrupt := &embeddingmedia.Error{Status: http.StatusBadGateway, Code: "embedding_corrupt_result"}
	if r == nil || len(r.Results) == 0 || len(r.Results) > documents || (topN > 0 && len(r.Results) > int(topN)) {
		return corrupt
	}
	seen := make(map[int32]bool, len(r.Results))
	for _, d := range r.Results {
		if d.Index < 0 || int(d.Index) >= documents || seen[d.Index] {
			return corrupt
		}
		seen[d.Index] = true
		if math.IsNaN(float64(d.RelevanceScore)) || math.IsInf(float64(d.RelevanceScore), 0) {
			return corrupt
		}
	}
	return nil
}
