// SPDX-License-Identifier: MIT
package jina_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/jina"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeReranker is the loaded gRPC peer. It implements the text RPC and the
// typed RPC so routing runs through the production loader and session.
type fakeReranker struct {
	grpcpkg.Backend
	mu         sync.Mutex
	metadata   func() (*pb.ModelMetadataResponse, error)
	typed      func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error)
	legacy     func(context.Context, *pb.RerankRequest) (*pb.RerankResult, error)
	typedCalls atomic.Int32
	textCalls  atomic.Int32
	typedSeen  []*pb.MultimodalRerankRequest
	textSeen   []*pb.RerankRequest
}

func (b *fakeReranker) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *fakeReranker) IsBusy() bool                              { return false }
func (b *fakeReranker) Free(context.Context) error                { return nil }
func (b *fakeReranker) ModelMetadata(context.Context, *pb.ModelOptions, ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return b.metadata()
}
func (b *fakeReranker) MultimodalRerank(ctx context.Context, in *pb.MultimodalRerankRequest, _ ...ggrpc.CallOption) (*pb.MultimodalRerankResponse, error) {
	b.typedCalls.Add(1)
	b.mu.Lock()
	b.typedSeen = append(b.typedSeen, in)
	b.mu.Unlock()
	return b.typed(ctx, in)
}
func (b *fakeReranker) Rerank(ctx context.Context, in *pb.RerankRequest, _ ...ggrpc.CallOption) (*pb.RerankResult, error) {
	b.textCalls.Add(1)
	b.mu.Lock()
	b.textSeen = append(b.textSeen, in)
	b.mu.Unlock()
	return b.legacy(ctx, in)
}

type fakeAcquirer struct {
	calls atomic.Int32
	fn    func(embeddingmedia.Source) ([]byte, error)
}

func (a *fakeAcquirer) Acquire(_ context.Context, s embeddingmedia.Source, budget *embeddingmedia.Budget) ([]byte, error) {
	a.calls.Add(1)
	data := []byte(s.Kind + ":" + s.URL + s.Data)
	if a.fn != nil {
		var err error
		if data, err = a.fn(s); err != nil {
			return nil, err
		}
	}
	return data, budget.Consume(embeddingmedia.Consumption{MediaBytes: uint64(len(data))})
}

func rerankMeta(mod ...pb.EmbeddingModality) *pb.ModelMetadataResponse {
	return &pb.ModelMetadataResponse{RerankProtocolVersion: 1, EmbeddingGeneration: "g1", RerankModalities: mod}
}

// ranked answers every document with a descending score, document 0 first.
func ranked(n int) *pb.RerankResult {
	r := &pb.RerankResult{Usage: &pb.Usage{TotalTokens: 11, PromptTokens: 11}}
	for i := 0; i < n; i++ {
		r.Results = append(r.Results, &pb.DocumentResult{Index: int32(i), RelevanceScore: float32(n-i) / 10, Text: "backend text must not be shown"})
	}
	return r
}

type rerankHarness struct {
	e       *echo.Echo
	be      *fakeReranker
	acq     *fakeAcquirer
	limiter *embeddingmedia.Limiter
}

func newRerankHarness() *rerankHarness {
	dir := GinkgoT().TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	app := config.NewApplicationConfig()
	app.Context = ctx
	app.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
	yes := true
	cfg := config.ModelConfig{Name: "rr", Backend: "fake"}
	cfg.Reranking = &yes
	cfg.Model = "rr.gguf"
	cl := config.NewModelConfigLoader(dir)
	cl.ReplaceModelConfigs([]config.ModelConfig{cfg})
	be := &fakeReranker{
		metadata: func() (*pb.ModelMetadataResponse, error) {
			return rerankMeta(pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE), nil
		},
		typed: func(_ context.Context, in *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
			r := ranked(len(in.Documents))
			if in.TopN > 0 && int(in.TopN) < len(r.Results) {
				r.Results = r.Results[:in.TopN]
			}
			return &pb.MultimodalRerankResponse{Result: r, Consumption: &pb.EmbeddingConsumption{Pixels: 4}}, nil
		},
		legacy: func(_ context.Context, in *pb.RerankRequest) (*pb.RerankResult, error) {
			r := ranked(len(in.Documents))
			for _, d := range r.Results {
				d.Text = in.Documents[d.Index]
			}
			return r, nil
		},
	}
	ml := model.NewModelLoader(app.SystemState)
	ml.SetModelRouter(func(_ context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
		return model.NewModelWithClient(id, "test://rerank", be), nil
	})
	acq := &fakeAcquirer{}
	re := middleware.NewRequestExtractor(cl, ml, app)
	limiter := embeddingmedia.NewLimiter(1)
	handler := jina.JINARerankEndpoint(cl, ml, app, jina.WithMediaAcquirer(acq), jina.WithPreparationLimiter(limiter))
	e := echo.New()
	e.POST("/v1/rerank", handler, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.JINARerankRequest) }))
	return &rerankHarness{e: e, be: be, acq: acq, limiter: limiter}
}

func (h *rerankHarness) post(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.e.ServeHTTP(rec, req)
	return rec
}

const pngURL = "data:image/png;base64,iVBORw0KGgo="

func imagePart() string        { return `{"type":"image_url","image_url":{"url":"` + pngURL + `"}}` }
func textPart(s string) string { return `{"type":"text","text":"` + s + `"}` }

type rerankBody struct {
	Model   string `json:"model"`
	Results []struct {
		Index    int `json:"index"`
		Document struct {
			Text string `json:"text"`
		} `json:"document"`
		Score float64 `json:"relevance_score"`
	} `json:"results"`
	Usage struct {
		Total int `json:"total_tokens"`
	} `json:"usage"`
}

var _ = Describe("Multimodal rerank HTTP", func() {
	var h *rerankHarness
	BeforeEach(func() { h = newRerankHarness() })

	It("keeps the text request on the text RPC with the same fields as before", func() {
		rec := h.post(`{"model":"rr","query":"q","documents":["a","b","c"],"top_n":2}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var out rerankBody
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Results).To(HaveLen(3))
		Expect(out.Results[0].Document.Text).To(Equal("a"))
		Expect(h.be.textCalls.Load()).To(Equal(int32(1)))
		Expect(h.be.typedCalls.Load()).To(BeZero())
		Expect(h.be.textSeen[0].Query).To(Equal("q"))
		Expect(h.be.textSeen[0].TopN).To(Equal(int32(2)))
		Expect(h.acq.calls.Load()).To(BeZero())
	})

	It("sends a text query with text and image documents through the typed RPC in order", func() {
		rec := h.post(`{"model":"rr","query":"a red cat","documents":["a dog",{"content":[` + textPart("a cat") + `,` + imagePart() + `]},{"content":[` + imagePart() + `]}]}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var out rerankBody
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Results).To(HaveLen(3))
		Expect(out.Usage.Total).To(Equal(11))
		// Document text is what the caller sent, never the backend's text or a media location.
		Expect(out.Results[0].Document.Text).To(Equal("a dog"))
		Expect(out.Results[1].Document.Text).To(Equal("a cat"))
		Expect(out.Results[2].Document.Text).To(BeEmpty())
		Expect(h.be.textCalls.Load()).To(BeZero(), "media must never fall back to the text RPC")
		Expect(h.be.typedCalls.Load()).To(Equal(int32(1)))
		sent := h.be.typedSeen[0]
		Expect(sent.Model).To(Equal("rr.gguf"))
		Expect(sent.EmbeddingGeneration).To(Equal("g1"))
		Expect(sent.Query).To(HaveLen(1))
		Expect(sent.Documents).To(HaveLen(3))
		Expect(sent.Documents[1].Parts).To(HaveLen(2))
		Expect(sent.Documents[1].Parts[1].GetMedia().GetKind()).To(Equal(pb.EmbeddingMedia_IMAGE))
		Expect(string(sent.Documents[1].Parts[1].GetMedia().GetData())).To(HavePrefix("image:"))
		Expect(sent.Budget.RemainingSourceParts).To(Equal(embeddingmedia.MaxSourceParts), "backend gets the full remaining allowance")
		Expect(h.acq.calls.Load()).To(Equal(int32(2)))
	})

	It("applies top_n to a typed request", func() {
		rec := h.post(`{"model":"rr","query":"q","documents":[{"content":[` + imagePart() + `]},"b","c"],"top_n":2}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(h.be.typedSeen[0].TopN).To(Equal(int32(2)))
		rec = h.post(`{"model":"rr","query":"q","documents":[{"content":[` + imagePart() + `]}],"top_n":0}`)
		Expect(rec.Code).To(Equal(http.StatusUnprocessableEntity))
	})

	It("sends text-only content objects to the text RPC because nothing can be dropped", func() {
		rec := h.post(`{"model":"rr","query":{"content":[` + textPart("a ") + `,` + textPart("cat") + `]},"documents":[{"content":[` + textPart("x") + `]},"y"]}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(h.be.typedCalls.Load()).To(BeZero())
		Expect(h.be.textCalls.Load()).To(Equal(int32(1)))
		Expect(h.be.textSeen[0].Query).To(Equal("a cat"))
		Expect(h.be.textSeen[0].Documents).To(Equal([]string{"x", "y"}))
	})

	Describe("status mapping", func() {
		img := func() string { return `{"model":"rr","query":"q","documents":[{"content":[` + imagePart() + `]}]}` }
		It("400 on malformed structure and 422 on audio, before any fetch", func() {
			Expect(h.post(`{"model":"rr","query":"q","documents":[{"content":[]}]}`).Code).To(Equal(400))
			Expect(h.post(`{"model":"rr","query":"q","documents":[{"content":[` + imagePart() + `,` + textPart("late") + `]}]}`).Code).To(Equal(400))
			Expect(h.post(`{"model":"rr","query":"q","documents":[{"content":[{"type":"input_audio","input_audio":{"data":"YQ=="}}]}]}`).Code).To(Equal(422))
			Expect(h.acq.calls.Load()).To(BeZero())
			Expect(h.be.typedCalls.Load()).To(BeZero())
		})
		It("422 when the reranker publishes no image input, with no fetch and no text score", func() {
			h.be.metadata = func() (*pb.ModelMetadataResponse, error) { return rerankMeta(pb.EmbeddingModality_TEXT), nil }
			Expect(h.post(img()).Code).To(Equal(422))
			Expect(h.acq.calls.Load()).To(BeZero())
			Expect(h.be.textCalls.Load()).To(BeZero())
			Expect(h.be.typedCalls.Load()).To(BeZero())
		})
		It("501 when the backend does not publish the rerank protocol", func() {
			h.be.metadata = func() (*pb.ModelMetadataResponse, error) {
				return &pb.ModelMetadataResponse{EmbeddingGeneration: "g1"}, nil
			}
			Expect(h.post(img()).Code).To(Equal(501))
			Expect(h.be.textCalls.Load()).To(BeZero())
		})
		It("501 when the worker answers UNIMPLEMENTED", func() {
			h.be.typed = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				return nil, status.Error(codes.Unimplemented, "old")
			}
			Expect(h.post(img()).Code).To(Equal(501))
			Expect(h.be.textCalls.Load()).To(BeZero())
		})
		It("maps backend limit and media errors", func() {
			for code, want := range map[codes.Code]int{codes.ResourceExhausted: 429, codes.DeadlineExceeded: 504, codes.FailedPrecondition: 422, codes.InvalidArgument: 400} {
				h.be.typed = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
					return nil, status.Error(code, "x")
				}
				Expect(h.post(img()).Code).To(Equal(want), code.String())
			}
			h.be.typed = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				return nil, status.Error(codes.ResourceExhausted, "limit_exceeded: pixels")
			}
			Expect(h.post(img()).Code).To(Equal(413))
			h.be.typed = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				return nil, status.Error(codes.InvalidArgument, "unsupported_media_format: gif")
			}
			Expect(h.post(img()).Code).To(Equal(415))
		})
		It("502 on a fetch failure and 413 when the fetched media exceeds the allowance", func() {
			h.acq.fn = func(embeddingmedia.Source) ([]byte, error) {
				return nil, &embeddingmedia.Error{Status: 502, Code: "embedding_fetch_failed"}
			}
			Expect(h.post(img()).Code).To(Equal(502))
			Expect(h.be.typedCalls.Load()).To(BeZero())
			h.acq.fn = func(embeddingmedia.Source) ([]byte, error) {
				return make([]byte, embeddingmedia.MaxMediaBytes+1), nil
			}
			Expect(h.post(img()).Code).To(Equal(413))
		})
		It("502 when the backend reports no consumption for media, or more than it was given", func() {
			h.be.typed = func(_ context.Context, in *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				return &pb.MultimodalRerankResponse{Result: ranked(len(in.Documents))}, nil
			}
			Expect(h.post(img()).Code).To(Equal(502))
			h.be.typed = func(_ context.Context, in *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				return &pb.MultimodalRerankResponse{Result: ranked(len(in.Documents)), Consumption: &pb.EmbeddingConsumption{Pixels: embeddingmedia.MaxPixels + 1}}, nil
			}
			Expect(h.post(img()).Code).To(Equal(502))
		})
		It("502 on an impossible result: repeated index, out of range, extra rows, NaN", func() {
			bad := []*pb.RerankResult{
				{Results: []*pb.DocumentResult{{Index: 0, RelevanceScore: 1}, {Index: 0, RelevanceScore: 0.5}}},
				{Results: []*pb.DocumentResult{{Index: 5, RelevanceScore: 1}}},
				{Results: []*pb.DocumentResult{{Index: 0, RelevanceScore: 1}, {Index: 1, RelevanceScore: 1}}},
				{},
			}
			for _, r := range bad {
				h.be.typed = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
					return &pb.MultimodalRerankResponse{Result: r, Consumption: &pb.EmbeddingConsumption{}}, nil
				}
				Expect(h.post(img()).Code).To(Equal(502))
			}
		})
		It("429 when the preparation slot is taken, before any fetch", func() {
			release, err := h.limiter.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			defer release()
			Expect(h.post(img()).Code).To(Equal(429))
			Expect(h.acq.calls.Load()).To(BeZero())
			// Text requests do not use a preparation slot.
			Expect(h.post(`{"model":"rr","query":"q","documents":["a"]}`).Code).To(Equal(200))
		})
	})
})
