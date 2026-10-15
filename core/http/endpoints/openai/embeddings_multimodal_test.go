// SPDX-License-Identifier: MIT
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeEmbedBackend is the loaded gRPC peer. It implements the legacy and the
// typed multimodal RPC so routing exercises the production loader and session.
type fakeEmbedBackend struct {
	grpcpkg.Backend
	mu           sync.Mutex
	metadata     func() (*pb.ModelMetadataResponse, error)
	multimodal   func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error)
	legacy       func(context.Context, *pb.PredictOptions) (*pb.EmbeddingResult, error)
	metadataN    atomic.Int32
	multimodalN  atomic.Int32
	legacyN      atomic.Int32
	requests     []*pb.MultimodalEmbeddingRequest
	legacyInputs []*pb.PredictOptions
}

func (b *fakeEmbedBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *fakeEmbedBackend) IsBusy() bool                              { return false }
func (b *fakeEmbedBackend) Free(context.Context) error                { return nil }
func (b *fakeEmbedBackend) ModelMetadata(context.Context, *pb.ModelOptions, ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	b.metadataN.Add(1)
	return b.metadata()
}
func (b *fakeEmbedBackend) MultimodalEmbedding(ctx context.Context, in *pb.MultimodalEmbeddingRequest, _ ...ggrpc.CallOption) (*pb.MultimodalEmbeddingResponse, error) {
	b.multimodalN.Add(1)
	b.mu.Lock()
	b.requests = append(b.requests, in)
	b.mu.Unlock()
	return b.multimodal(ctx, in)
}
func (b *fakeEmbedBackend) Embeddings(ctx context.Context, in *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	b.legacyN.Add(1)
	b.mu.Lock()
	b.legacyInputs = append(b.legacyInputs, in)
	b.mu.Unlock()
	return b.legacy(ctx, in)
}
func (b *fakeEmbedBackend) sent() []*pb.MultimodalEmbeddingRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*pb.MultimodalEmbeddingRequest(nil), b.requests...)
}

func allModalities() []pb.EmbeddingModality {
	return []pb.EmbeddingModality{pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE, pb.EmbeddingModality_AUDIO, pb.EmbeddingModality_SILENT_VIDEO, pb.EmbeddingModality_AUDIOVISUAL_VIDEO}
}
func metadataWith(mod ...pb.EmbeddingModality) *pb.ModelMetadataResponse {
	return &pb.ModelMetadataResponse{EmbeddingProtocolVersion: 1, EmbeddingGeneration: "g1", EmbeddingModalities: mod}
}
func finalResult(v ...float32) *pb.MultimodalEmbeddingResponse {
	return &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: v, Tokens: 1, Dim: int32(len(v)), Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}}
}

type acquireCall struct {
	Source embeddingmedia.Source
}

// fakeAcquirer stands in for the network. It charges the budget like the real
// acquirer: only on success, only the media bytes.
type fakeAcquirer struct {
	mu    sync.Mutex
	calls []acquireCall
	fn    func(ctx context.Context, s embeddingmedia.Source) ([]byte, error)
	// noCharge models an acquirer that returns bytes without charging the
	// budget; the endpoint must still bound what it dispatches.
	noCharge bool
}

func (a *fakeAcquirer) Acquire(ctx context.Context, s embeddingmedia.Source, budget *embeddingmedia.Budget) ([]byte, error) {
	a.mu.Lock()
	a.calls = append(a.calls, acquireCall{s})
	fn := a.fn
	a.mu.Unlock()
	var data []byte
	var err error
	if fn != nil {
		data, err = fn(ctx, s)
	} else {
		key := s.URL
		if key == "" {
			key = s.Data
		}
		data = []byte(s.Kind + ":" + key)
	}
	if err != nil {
		return nil, err
	}
	if !a.noCharge {
		if err := budget.Consume(embeddingmedia.Consumption{MediaBytes: uint64(len(data))}); err != nil {
			return nil, err
		}
	}
	return data, nil
}
func (a *fakeAcquirer) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

type harness struct {
	e       *echo.Echo
	be      *fakeEmbedBackend
	acq     *fakeAcquirer
	limiter *embeddingmedia.Limiter
	app     *config.ApplicationConfig
}

var embeddingAliases = []string{"/v1/embeddings", "/embeddings", "/v1/engines/emb/embeddings"}

func newHarness(apiKeys ...string) *harness {
	dir := GinkgoT().TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	app := config.NewApplicationConfig()
	app.Context = ctx
	app.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
	app.ApiKeys = apiKeys
	yes := true
	cfg := config.ModelConfig{Name: "emb", Backend: "fake", Embeddings: &yes}
	cfg.Model = "emb.gguf"
	cl := config.NewModelConfigLoader(dir)
	cl.ReplaceModelConfigs([]config.ModelConfig{cfg})
	be := &fakeEmbedBackend{
		metadata: func() (*pb.ModelMetadataResponse, error) { return metadataWith(allModalities()...), nil },
		multimodal: func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			return finalResult(1, 2, 3), nil
		},
		legacy: func(context.Context, *pb.PredictOptions) (*pb.EmbeddingResult, error) {
			return &pb.EmbeddingResult{Embeddings: []float32{0.5, 0.25}, Tokens: 1, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}, nil
		},
	}
	ml := model.NewModelLoader(app.SystemState)
	ml.SetModelRouter(func(_ context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
		return model.NewModelWithClient(id, "test://embeddings", be), nil
	})
	acq := &fakeAcquirer{}
	limiter := embeddingmedia.NewLimiter(1)
	re := middleware.NewRequestExtractor(cl, ml, app)
	handler := EmbeddingsEndpoint(cl, ml, templates.NewEvaluator(dir), app, WithMediaAcquirer(acq), WithPreparationLimiter(limiter))
	e := echo.New()
	chain := []echo.MiddlewareFunc{
		auth.Middleware(nil, app),
		re.SetModelAndConfigWithDefault(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }, config.BuildUsecaseFilterFn(config.FLAG_EMBEDDINGS)),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}
	for _, p := range []string{"/v1/embeddings", "/embeddings", "/v1/engines/:model/embeddings"} {
		e.POST(p, handler, chain...)
	}
	return &harness{e: e, be: be, acq: acq, limiter: limiter, app: app}
}

func (h *harness) post(path, body string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	h.e.ServeHTTP(rec, req)
	return rec
}

// vecItem is the client's view of one data entry: the server marshals the
// embedding as an array or a base64 string and Item has no decoder.
type vecItem struct {
	Index           int
	Object          string
	Embedding       []float32
	EmbeddingBase64 string
}

func (h *harness) vectors(rec *httptest.ResponseRecorder) []vecItem {
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	var resp struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Data   []struct {
			Index     int             `json:"index"`
			Object    string          `json:"object"`
			Embedding json.RawMessage `json:"embedding"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
	Expect(resp.Object).To(Equal("list"))
	Expect(resp.Model).To(Equal("emb"))
	out := []vecItem{}
	for _, d := range resp.Data {
		it := vecItem{Index: d.Index, Object: d.Object}
		if len(d.Embedding) > 0 && d.Embedding[0] == '"' {
			Expect(json.Unmarshal(d.Embedding, &it.EmbeddingBase64)).To(Succeed())
		} else {
			Expect(json.Unmarshal(d.Embedding, &it.Embedding)).To(Succeed())
		}
		out = append(out, it)
	}
	return out
}

const imageData = "data:image/png;base64,iVBORw0KGgo="

func structuredBody(items ...string) string {
	return `{"model":"emb","input":[` + strings.Join(items, ",") + `]}`
}
func content(parts ...string) string { return `{"content":[` + strings.Join(parts, ",") + `]}` }
func textPart(s string) string {
	b, _ := json.Marshal(s)
	return `{"type":"text","text":` + string(b) + `}`
}
func imagePart(url string) string {
	return `{"type":"image_url","image_url":{"url":` + fmt.Sprintf("%q", url) + `}}`
}

var _ = Describe("Multimodal HTTP", func() {
	var h *harness
	BeforeEach(func() { h = newHarness() })

	It("serves both documented aliases and the engine alias with one vector per item", func() {
		for _, path := range embeddingAliases {
			h.be.multimodal = func(_ context.Context, in *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				return finalResult(float32(len(in.Parts)), 1), nil
			}
			rec := h.post(path, structuredBody(content(textPart("a"), imagePart(imageData)), `"b"`, content(textPart("c"))))
			items := h.vectors(rec)
			Expect(items).To(HaveLen(3), path)
			for i, want := range []float32{2, 1, 1} {
				Expect(items[i].Index).To(Equal(i))
				Expect(items[i].Object).To(Equal("embedding"))
				Expect(items[i].Embedding).To(Equal([]float32{want, 1}))
			}
		}
	})

	It("denies unauthenticated requests before any metadata read or fetch", func() {
		h = newHarness("sk-test-key")
		for _, path := range embeddingAliases {
			rec := h.post(path, structuredBody(content(imagePart("https://media.example.invalid/a.png"))))
			Expect(rec.Code).To(Equal(http.StatusUnauthorized), path)
		}
		Expect(h.be.metadataN.Load()).To(BeZero())
		Expect(h.acq.count()).To(BeZero())
		rec := h.post("/v1/embeddings", structuredBody(content(textPart("x"))), func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer sk-test-key")
		})
		Expect(h.vectors(rec)).To(HaveLen(1))
	})

	It("checks selected loaded-model metadata before fetching any media", func() {
		h.be.metadata = func() (*pb.ModelMetadataResponse, error) {
			return metadataWith(pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE), nil
		}
		rec := h.post("/v1/embeddings", structuredBody(content(imagePart("https://media.example.invalid/a.png"), `{"type":"input_audio","input_audio":{"url":"https://media.example.invalid/a.wav"}}`)))
		Expect(rec.Code).To(Equal(422))
		Expect(h.acq.count()).To(BeZero(), "an unsupported modality must be refused before any fetch")
		Expect(h.be.multimodalN.Load()).To(BeZero())
		Expect(h.be.metadataN.Load()).To(BeNumerically(">=", 1))
		Expect(h.limiter.TryAcquire()).NotTo(BeNil(), "slot must be free")
	})

	It("passes every part shape in order without implicit template or prefix", func() {
		h.acq.fn = func(_ context.Context, s embeddingmedia.Source) ([]byte, error) {
			return []byte("bytes-of-" + s.Kind + "-" + s.Format), nil
		}
		body := structuredBody(
			content(
				textPart("first"),
				imagePart("https://media.example.invalid/p.png"),
				textPart(""),
				`{"type":"input_audio","input_audio":{"data":"UklGRg==","format":"wav"}}`,
				`{"type":"input_video","input_video":{"url":"https://media.example.invalid/v.mp4"}}`,
				`{"type":"video_url","video_url":{"data":"AAAA","format":"mp4"}}`,
				textPart("last"),
			),
			`"plain string"`,
		)
		h.vectors(h.post("/v1/embeddings", body))
		sent := h.be.sent()
		Expect(sent).To(HaveLen(2))
		type shape struct {
			text string
			kind pb.EmbeddingMedia_Kind
			data string
		}
		var got []shape
		for _, p := range sent[0].Parts {
			switch v := p.Part.(type) {
			case *pb.EmbeddingPart_Text:
				got = append(got, shape{text: v.Text})
			case *pb.EmbeddingPart_Media:
				got = append(got, shape{kind: v.Media.Kind, data: string(v.Media.Data)})
			}
		}
		Expect(got).To(Equal([]shape{
			{text: "first"},
			{kind: pb.EmbeddingMedia_IMAGE, data: "bytes-of-image-"},
			{text: ""},
			{kind: pb.EmbeddingMedia_AUDIO, data: "bytes-of-audio-wav"},
			{kind: pb.EmbeddingMedia_VIDEO, data: "bytes-of-video-"},
			{kind: pb.EmbeddingMedia_VIDEO, data: "bytes-of-video-mp4"},
			{text: "last"},
		}))
		Expect(sent[1].Parts).To(HaveLen(1))
		Expect(sent[1].Parts[0].GetText()).To(Equal("plain string"))
		Expect(sent[0].Model).To(Equal("emb.gguf"))
		Expect(sent[0].EmbeddingGeneration).To(Equal("g1"))
	})

	It("keeps request indexes for a reordered mixed batch", func() {
		h.be.multimodal = func(_ context.Context, in *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			switch p := in.Parts[0].Part.(type) {
			case *pb.EmbeddingPart_Text:
				return finalResult(10, float32(len(p.Text))), nil
			default:
				return finalResult(20, 1), nil
			}
		}
		rec := h.post("/v1/embeddings", structuredBody(`"abc"`, content(imagePart(imageData)), `"de"`, content(textPart("f"))))
		items := h.vectors(rec)
		Expect(items).To(HaveLen(4))
		want := [][]float32{{10, 3}, {20, 1}, {10, 2}, {10, 1}}
		for i := range want {
			Expect(items[i].Index).To(Equal(i))
			Expect(items[i].Embedding).To(Equal(want[i]))
		}
	})

	It("returns no partial envelope when a later item fails", func() {
		var n atomic.Int32
		h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			if n.Add(1) == 3 {
				return nil, status.Error(codes.Internal, "boom")
			}
			return finalResult(1, 2), nil
		}
		rec := h.post("/v1/embeddings", structuredBody(content(textPart("a")), content(textPart("b")), content(textPart("c"))))
		Expect(rec.Code).To(Equal(502))
		Expect(rec.Body.String()).NotTo(ContainSubstring(`"embedding"`))
		Expect(rec.Body.String()).NotTo(ContainSubstring(`"data"`))
		Expect(n.Load()).To(Equal(int32(3)))
	})

	Describe("status mapping", func() {
		type tc struct {
			name string
			want int
			body string
			set  func(*harness)
		}
		url := "https://media.example.invalid/x.png"
		img := structuredBody(content(imagePart(url)))
		typed := func(code int) func(*harness) {
			return func(h *harness) {
				h.acq.fn = func(context.Context, embeddingmedia.Source) ([]byte, error) {
					return nil, &embeddingmedia.Error{Status: code, Code: "typed"}
				}
			}
		}
		grpcErr := func(c codes.Code) func(*harness) {
			return func(h *harness) {
				h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
					return nil, status.Error(c, "backend detail with private-token")
				}
			}
		}
		cases := []tc{
			{"400 invalid structure rejects unknown content fields", 400, `{"model":"emb","input":[{"content":[{"type":"text","text":"x","extra":1}]}]}`, nil},
			{"400 empty content array", 400, `{"model":"emb","input":[{"content":[]}]}`, nil},
			{"400 structured input conflicts with messages", 400, `{"model":"emb","input":[{"content":[{"type":"text","text":"x"}]}],"messages":[{"role":"user","content":"hi"}]}`, nil},
			{"400 backend invalid argument", 400, img, grpcErr(codes.InvalidArgument)},
			{"413 acquisition limit", 413, img, typed(413)},
			{"413 backend resource exhausted", 413, img, grpcErr(codes.ResourceExhausted)},
			{"415 unsupported media format", 415, img, typed(415)},
			{"422 model cannot consume the modality", 422, img, func(h *harness) {
				h.be.metadata = func() (*pb.ModelMetadataResponse, error) { return metadataWith(pb.EmbeddingModality_TEXT), nil }
			}},
			{"422 backend refuses after probing", 422, img, grpcErr(codes.FailedPrecondition)},
			{"429 preparation capacity exhausted", 429, img, func(h *harness) {
				_, err := h.limiter.TryAcquire()
				Expect(err).NotTo(HaveOccurred())
			}},
			{"501 old backend metadata", 501, img, func(h *harness) {
				h.be.metadata = func() (*pb.ModelMetadataResponse, error) { return &pb.ModelMetadataResponse{}, nil }
			}},
			{"501 unimplemented multimodal RPC", 501, img, grpcErr(codes.Unimplemented)},
			{"502 fetch failure", 502, img, typed(502)},
			{"502 corrupt backend result", 502, img, func(h *harness) {
				h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
					return &pb.MultimodalEmbeddingResponse{}, nil
				}
			}},
			{"502 inference transport failure", 502, img, grpcErr(codes.Unavailable)},
			{"504 fetch timeout", 504, img, typed(504)},
			{"504 inference deadline", 504, img, grpcErr(codes.DeadlineExceeded)},
		}
		for _, c := range cases {
			c := c
			It(c.name, func() {
				if c.set != nil {
					c.set(h)
				}
				for _, path := range []string{"/v1/embeddings", "/embeddings"} {
					rec := h.post(path, c.body)
					Expect(rec.Code).To(Equal(c.want), rec.Body.String())
					Expect(rec.Body.String()).NotTo(ContainSubstring("private-token"))
					Expect(rec.Body.String()).NotTo(ContainSubstring("media.example.invalid"))
				}
				// A held slot belongs to the 429 case itself; every other exit frees it.
				if c.want != 429 {
					release, err := h.limiter.TryAcquire()
					Expect(err).NotTo(HaveOccurred(), "slot leaked")
					release()
				}
			})
		}
	})

	Describe("result validation", func() {
		bad := map[string]*pb.MultimodalEmbeddingResponse{
			"nil result":            {},
			"empty vector":          {Result: &pb.EmbeddingResult{Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}},
			"NaN value":             finalResult(1, float32(math.NaN())),
			"infinite value":        finalResult(1, float32(math.Inf(1))),
			"zero vector":           finalResult(0, 0, 0),
			"dimension mismatch":    {Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2, 3}, Tokens: 1, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}},
			"final with 2 tokens":   {Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2, 3, 4}, Tokens: 2, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}},
			"per-token shape wrong": {Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2, 3}, Tokens: 2, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN}},
		}
		for name, res := range bad {
			name, res := name, res
			It("rejects a "+name+" with 502 and no envelope", func() {
				h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
					return res, nil
				}
				rec := h.post("/v1/embeddings", structuredBody(content(textPart("a"))))
				Expect(rec.Code).To(Equal(502), rec.Body.String())
				Expect(rec.Body.String()).NotTo(ContainSubstring(`"embedding"`))
			})
		}
		It("rejects an undeclared layout when Go-side pooling needs per-token vectors, as the legacy path does", func() {
			h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				return &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{1, 2}, Tokens: 1, Dim: 2}}, nil
			}
			rec := h.post("/v1/embeddings", `{"model":"emb","pooling":"mean","input":[{"content":[{"type":"text","text":"a"}]}]}`)
			Expect(rec.Code).To(Equal(400), rec.Body.String())
		})
		It("pools per-token results exactly once with the model's scheme", func() {
			h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				return &pb.MultimodalEmbeddingResponse{Result: &pb.EmbeddingResult{Embeddings: []float32{1, 0, 0, 1}, Tokens: 2, Dim: 2, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN}}, nil
			}
			rec := h.post("/v1/embeddings", `{"model":"emb","pooling":"last","input":["a",{"content":[{"type":"text","text":"b"}]}]}`)
			items := h.vectors(rec)
			Expect(items).To(HaveLen(2))
			for _, it := range items {
				Expect(it.Embedding).To(HaveLen(2))
				Expect(it.Embedding[0]).To(BeNumerically("~", 0, 1e-6))
				Expect(it.Embedding[1]).To(BeNumerically("~", 1, 1e-6))
			}
			Expect(h.be.multimodalN.Load()).To(Equal(int32(2)))
		})
		It("encodes base64 float32 little endian when requested", func() {
			rec := h.post("/v1/embeddings", `{"model":"emb","encoding_format":"base64","input":[{"content":[{"type":"text","text":"b"}]}]}`)
			items := h.vectors(rec)
			raw, err := base64.StdEncoding.DecodeString(items[0].EmbeddingBase64)
			Expect(err).NotTo(HaveOccurred())
			Expect(binary.LittleEndian.Uint32(raw[:4])).To(Equal(math.Float32bits(1)))
			Expect(items[0].Embedding).To(BeEmpty())
		})
	})

	Describe("resource accounting", func() {
		It("supplies remaining budgets per item and deducts actual consumption between items", func() {
			h.acq.fn = func(context.Context, embeddingmedia.Source) ([]byte, error) { return make([]byte, 10), nil }
			var n atomic.Int32
			h.be.multimodal = func(_ context.Context, in *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				res := finalResult(1, 2)
				if n.Add(1) == 1 {
					res.Consumption = &pb.EmbeddingConsumption{Frames: 100, Pixels: 1000, AudioAllocationBytes: 77, SourceDurationMilliseconds: 5000, TemporaryBytes: 9, CompressedBytes: 10, TextBytes: 1, SourceParts: 2}
				}
				return res, nil
			}
			h.vectors(h.post("/v1/embeddings", structuredBody(content(textPart("a"), imagePart(imageData)), content(imagePart(imageData)))))
			sent := h.be.sent()
			Expect(sent).To(HaveLen(2))
			first, second := sent[0].Budget, sent[1].Budget
			Expect(first.RemainingFrames).To(Equal(embeddingmedia.MaxFrames))
			Expect(first.RemainingCompressedBytes).To(Equal(embeddingmedia.MaxMediaBytes))
			Expect(first.RemainingTextBytes).To(Equal(embeddingmedia.MaxTextBytes))
			Expect(first.RemainingSourceParts).To(Equal(embeddingmedia.MaxSourceParts))
			Expect(second.RemainingFrames).To(Equal(embeddingmedia.MaxFrames - 100))
			Expect(second.RemainingPixels).To(Equal(embeddingmedia.MaxPixels - 1000))
			Expect(second.RemainingAudioAllocationBytes).To(Equal(embeddingmedia.MaxAudioBytes - 77))
			Expect(second.RemainingSourceDurationMilliseconds).To(Equal(embeddingmedia.MaxDurationMilliseconds - 5000))
			Expect(second.RemainingTemporaryBytes).To(Equal(embeddingmedia.MaxTemporaryBytes - 9))
			Expect(second.RemainingCompressedBytes).To(Equal(embeddingmedia.MaxMediaBytes - 10))
			Expect(second.RemainingTextBytes).To(Equal(embeddingmedia.MaxTextBytes - 1))
			Expect(second.RemainingSourceParts).To(Equal(embeddingmedia.MaxSourceParts - 2))
			Expect(second.RemainingPreparationMilliseconds).To(BeNumerically("<=", embeddingmedia.MaxPreparationMilliseconds))
		})
		It("charges Go-known media bytes even when the backend under-reports", func() {
			h.acq.fn = func(context.Context, embeddingmedia.Source) ([]byte, error) { return make([]byte, 4096), nil }
			h.vectors(h.post("/v1/embeddings", structuredBody(content(imagePart(imageData)), `"b"`)))
			Expect(h.be.sent()[1].Budget.RemainingCompressedBytes).To(Equal(embeddingmedia.MaxMediaBytes - 4096))
		})
		It("treats consumption above the supplied remaining budget as a corrupt result", func() {
			var n atomic.Int32
			h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				res := finalResult(1, 2)
				if n.Add(1) == 1 {
					res.Consumption = &pb.EmbeddingConsumption{Frames: 100}
				} else {
					res.Consumption = &pb.EmbeddingConsumption{Frames: 21}
				}
				return res, nil
			}
			rec := h.post("/v1/embeddings", structuredBody(content(textPart("a")), content(textPart("b"))))
			Expect(rec.Code).To(Equal(502))
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"embedding"`))
		})
		It("rejects consumption wrapping past the maximum without unsigned wraparound", func() {
			h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				res := finalResult(1, 2)
				res.Consumption = &pb.EmbeddingConsumption{Pixels: math.MaxUint64}
				return res, nil
			}
			Expect(h.post("/v1/embeddings", structuredBody(content(textPart("a")))).Code).To(Equal(502))
		})
		It("rejects aggregate media over the request limit across items with 413", func() {
			h.acq.fn = func(context.Context, embeddingmedia.Source) ([]byte, error) {
				return make([]byte, 20<<20), nil
			}
			rec := h.post("/v1/embeddings", structuredBody(content(imagePart(imageData)), content(imagePart(imageData))))
			Expect(rec.Code).To(Equal(413), rec.Body.String())
			Expect(h.be.multimodalN.Load()).To(Equal(int32(1)))
		})
		It("validates proto.Size below the transport ceiling before dispatch", func() {
			h.acq.fn = func(context.Context, embeddingmedia.Source) ([]byte, error) {
				return make([]byte, 51<<20), nil
			}
			h.acq.noCharge = true
			rec := h.post("/v1/embeddings", structuredBody(content(imagePart(imageData))))
			Expect(rec.Code).To(Equal(413), rec.Body.String())
			Expect(h.be.multimodalN.Load()).To(BeZero())
		})
	})

	Describe("lifecycle", func() {
		It("holds the preparation slot during inference and releases it afterwards", func() {
			var duringErr error
			h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				var release func()
				release, duringErr = h.limiter.TryAcquire()
				if release != nil {
					release()
				}
				return finalResult(1, 2), nil
			}
			h.vectors(h.post("/v1/embeddings", structuredBody(content(textPart("a")))))
			var typed *embeddingmedia.Error
			Expect(duringErr).To(HaveOccurred())
			Expect(asMediaError(duringErr, &typed)).To(BeTrue())
			Expect(typed.Status).To(Equal(429))
			release, err := h.limiter.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			release()
		})
		It("releases the slot when inference fails", func() {
			h.be.multimodal = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				return nil, status.Error(codes.Internal, "x")
			}
			Expect(h.post("/v1/embeddings", structuredBody(content(textPart("a")))).Code).To(Equal(502))
			release, err := h.limiter.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			release()
		})
		It("propagates client cancellation to inference and returns no vectors", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			h.be.multimodal = func(c context.Context, _ *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				close(started)
				<-c.Done()
				return nil, c.Err()
			}
			go func() { <-started; cancel() }()
			rec := h.post("/v1/embeddings", structuredBody(content(textPart("a"))), func(r *http.Request) { *r = *r.WithContext(ctx) })
			Expect(rec.Code).To(Equal(499))
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"embedding"`))
			release, err := h.limiter.TryAcquire()
			Expect(err).NotTo(HaveOccurred(), "slot leaked after cancellation")
			release()
		})
		It("propagates client cancellation to a stalled fetch", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			h.acq.fn = func(c context.Context, _ embeddingmedia.Source) ([]byte, error) {
				close(started)
				<-c.Done()
				return nil, c.Err()
			}
			go func() { <-started; cancel() }()
			rec := h.post("/v1/embeddings", structuredBody(content(imagePart("https://media.example.invalid/x.png"))), func(r *http.Request) { *r = *r.WithContext(ctx) })
			Expect(rec.Code).To(Equal(499))
			Expect(h.be.multimodalN.Load()).To(BeZero())
			release, err := h.limiter.TryAcquire()
			Expect(err).NotTo(HaveOccurred())
			release()
		})
		It("maps an exceeded request deadline during inference to 504", func() {
			h.be.multimodal = func(c context.Context, _ *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
				<-c.Done()
				return nil, c.Err()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			rec := h.post("/v1/embeddings", structuredBody(content(textPart("a"))), func(r *http.Request) { *r = *r.WithContext(ctx) })
			Expect(rec.Code).To(Equal(504))
		})
	})

	It("keeps structured text, URLs and queries out of every log line", func() {
		var buf bytes.Buffer
		var mu sync.Mutex
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(&lockedWriter{&buf, &mu}, &slog.HandlerOptions{Level: slog.LevelDebug}), xlog.LogLevelDebug))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevelInfo, "text")) })
		body := structuredBody(
			`"private-string-input"`,
			content(textPart("private-part-text"), imagePart("https://media.example.invalid/a.png?private-query=1")),
		)
		h.post("/v1/embeddings", body)
		h.post("/v1/embeddings", structuredBody(content(imagePart("https://user:private-pass@media.example.invalid/a.png"))))
		h.acq.fn = func(context.Context, embeddingmedia.Source) ([]byte, error) {
			return nil, &embeddingmedia.Error{Status: 502, Code: "x"}
		}
		h.post("/v1/embeddings", body)
		mu.Lock()
		defer mu.Unlock()
		for _, secret := range []string{"private-string-input", "private-part-text", "private-pass", "private-query", "media.example.invalid"} {
			Expect(buf.String()).NotTo(ContainSubstring(secret))
		}
		Expect(buf.Len()).To(BeNumerically(">", 0), "debug logging must have been active")
	})
})

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func asMediaError(err error, target **embeddingmedia.Error) bool {
	e, ok := err.(*embeddingmedia.Error)
	if ok {
		*target = e
	}
	return ok
}

var _ = Describe("Legacy embedding response", func() {
	var h *harness
	BeforeEach(func() { h = newHarness() })
	leaves := func() {
		Expect(h.be.multimodalN.Load()).To(BeZero(), "legacy input must not use the typed RPC")
		Expect(h.be.metadataN.Load()).To(BeZero())
		Expect(h.acq.count()).To(BeZero())
	}
	It("embeds a string input through the legacy RPC unchanged", func() {
		items := h.vectors(h.post("/v1/embeddings", `{"model":"emb","input":"hello"}`))
		Expect(items).To(HaveLen(1))
		Expect(items[0].Embedding).To(Equal([]float32{0.5, 0.25}))
		Expect(h.be.legacyInputs[0].Embeddings).To(Equal("hello"))
		leaves()
	})
	It("embeds string arrays one vector per element with request indexes", func() {
		items := h.vectors(h.post("/embeddings", `{"model":"emb","input":["a","b"]}`))
		Expect(items).To(HaveLen(2))
		Expect(items[1].Index).To(Equal(1))
		Expect(h.be.legacyN.Load()).To(Equal(int32(2)))
		leaves()
	})
	It("embeds token arrays through EmbeddingTokens", func() {
		items := h.vectors(h.post("/v1/embeddings", `{"model":"emb","input":[[1,2,3]]}`))
		Expect(items).To(HaveLen(1))
		Expect(h.be.legacyInputs[0].EmbeddingTokens).To(Equal([]int32{1, 2, 3}))
		leaves()
	})
	It("renders messages into one legacy input", func() {
		items := h.vectors(h.post("/v1/embeddings", `{"model":"emb","messages":[{"role":"user","content":"hello there"}]}`))
		Expect(items).To(HaveLen(1))
		Expect(h.be.legacyInputs[0].Embeddings).To(ContainSubstring("hello there"))
		leaves()
	})
	It("returns base64 little endian floats when requested", func() {
		items := h.vectors(h.post("/v1/embeddings", `{"model":"emb","input":"x","encoding_format":"base64"}`))
		raw, err := base64.StdEncoding.DecodeString(items[0].EmbeddingBase64)
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(HaveLen(8))
		Expect(binary.LittleEndian.Uint32(raw[:4])).To(Equal(math.Float32bits(0.5)))
		leaves()
	})
	It("accepts the dimensions field without changing the returned vector", func() {
		items := h.vectors(h.post("/v1/embeddings", `{"model":"emb","input":"x","dimensions":1}`))
		Expect(items[0].Embedding).To(Equal([]float32{0.5, 0.25}))
		leaves()
	})
	It("keeps rejecting input together with messages", func() {
		rec := h.post("/v1/embeddings", `{"model":"emb","input":"x","messages":[{"role":"user","content":"y"}]}`)
		Expect(rec.Code).To(Equal(400))
		leaves()
	})
	It("keeps legacy backend errors flowing as before", func() {
		h.be.legacy = func(context.Context, *pb.PredictOptions) (*pb.EmbeddingResult, error) {
			return &pb.EmbeddingResult{Embeddings: []float32{1}, Layout: pb.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL}, nil
		}
		rec := h.post("/v1/embeddings", `{"model":"emb","pooling":"mean","input":"x"}`)
		Expect(rec.Code).To(Equal(400))
	})
})
