// SPDX-License-Identifier: MIT
package middleware_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/systemone"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
)

var _ = It("TestRetryRemoteImageMiddleware", func() {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte("image")) }))
	defer server.Close()
	GinkgoT().Setenv("HTTP_PROXY", server.URL)
	GinkgoT().Setenv("NO_PROXY", "")
	dir := GinkgoT().TempDir()
	cfg := newScoreRouterModel(dir, "smart-router")
	cfg.Router.Fallback = ""
	cfg.Router.Classifier = router.ClassifierDecisions
	app := &config.ApplicationConfig{Context: context.Background(), SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
	loader := config.NewModelConfigLoader(dir)
	req := openAIChat("")
	req.Messages[0].Content = []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "http://93.184.215.14/decision.png"}}}
	e := echo.New()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), httptest.NewRecorder())
	c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, req)
	c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
	re := NewRequestExtractor(loader, nil, app)
	// Same ordered schema parsing and routing middleware as registered chat.
	if err := re.SetOpenAIRequest(c); err != nil {
		Fail(fmt.Sprint(err))
	}
	defer req.Cancel()
	h := RouteModel(loader, app, nil, nil, OpenAIProbe, router.SourceChat, retryDecisionDeps())(func(echo.Context) error { Fail(fmt.Sprint("rejected image reached endpoint")); return nil })
	if err := h(c); err == nil || !strings.Contains(err.Error(), "images must be PNG or JPEG base64 data URLs") {
		Fail(fmt.Sprintf("expected shared image validation, got %v", err))
	}
	if got := calls.Load(); got != 0 {
		Fail(fmt.Sprintf("decision input performed %d network calls", got))
	}
})

var _ = It("TestRetryTypedOpenAIText", func() {
	var typed []schema.Content
	raw := []byte(`[{"type":"text","text":"first"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}},{"type":"text","text":"second"}]`)
	if err := json.Unmarshal(raw, &typed); err != nil {
		Fail(fmt.Sprint(err))
	}
	var untyped []any
	Expect(json.Unmarshal(raw, &untyped)).To(Succeed())
	a := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Role: "user", Content: typed}}})
	b := OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Role: "user", Content: untyped}}})
	if a.Prompt != b.Prompt || a.Prompt == "" {
		Fail(fmt.Sprintf("typed %q untyped %q", a.Prompt, b.Prompt))
	}
})

type retryDecisionFunc func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error)

func (f retryDecisionFunc) Decide(ctx context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
	return f(ctx, r)
}
func retryDecisionDeps() ClassifierDeps {
	return ClassifierDeps{
		ModelLookup: func(string) *config.ModelConfig {
			u := config.FLAG_DECISIONS
			return &config.ModelConfig{Backend: "llama-cpp", KnownUsecases: &u}
		},
		Decisions: func(string) backend.DecisionRunner {
			return retryDecisionFunc(func(context.Context, *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
				Fail(fmt.Sprint("invalid image reached runner"))
				return nil, nil
			})
		},
	}
}

var _ = It("TestRetryConstructionFailsClosed", func() {
	dir := GinkgoT().TempDir()
	cfg := newScoreRouterModel(dir, "smart-router")
	writeCandidate(dir, cfg.Router.Fallback)
	req := openAIChat("original")
	rec, err := runRouterWithDeps(config.NewModelConfigLoader(dir), &config.ApplicationConfig{Context: context.Background(), SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}, nil, cfg, req, ClassifierDeps{})
	if err == nil || !strings.Contains(err.Error(), "no scorer factory") || rec.Body.Len() != 0 {
		Fail(fmt.Sprintf("construction must fail closed: %v", err))
	}
	if req.Messages[0].Content != "original" {
		Fail(fmt.Sprint("payload changed"))
	}
})

var _ = It("TestRetryMaterialization", func() {
	for _, mode := range []string{"ordinary", "success", "fallback"} {
		By(mode)
		func() {
			dir := GinkgoT().TempDir()
			cfg := newScoreRouterModel(dir, "smart-router")
			writeCandidate(dir, "small-model")
			writeCandidate(dir, cfg.Router.Fallback)
			if mode == "ordinary" {
				cfg.Router = config.RouterConfig{}
			}
			req := openAIChat("")
			req.Messages[0].Content = []any{map[string]any{"type": "text", "text": "original"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AA=="}}}
			expectedImage := "AA=="
			if mode == "success" {
				var b bytes.Buffer
				if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
					Fail(fmt.Sprint(err))
				}
				expectedImage = base64.StdEncoding.EncodeToString(b.Bytes())
				req.Messages[0].Content.([]any)[1].(map[string]any)["image_url"] = map[string]any{"url": "data:image/png;base64," + expectedImage}
				raw, _ := json.Marshal(req.Messages[0].Content)
				var typed []schema.Content
				Expect(json.Unmarshal(raw, &typed)).To(Succeed())
				req.Messages[0].Content = typed
			}
			before, _ := json.Marshal(req.Messages[0].Content)
			app := &config.ApplicationConfig{Context: context.Background(), SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
			loader := config.NewModelConfigLoader(dir)
			c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())
			c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, req)
			c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
			if err := NewRequestExtractor(loader, nil, app).SetOpenAIRequest(c); err != nil {
				Fail(fmt.Sprint(err))
			}
			defer req.Cancel()
			if mode != "ordinary" && len(req.Messages[0].StringImages) != 0 {
				Fail(fmt.Sprint("premature image preparation"))
			}
			deps := ClassifierDeps{Scorer: func(string) backend.Scorer { return &stubScorer{} }}
			extractor := OpenAIProbe
			if mode == "success" {
				cfg.Router.Classifier = router.ClassifierDecisions
				deps = retryDecisionDeps()
				deps.Decisions = func(string) backend.DecisionRunner {
					return retryDecisionFunc(func(_ context.Context, r *schema.SystemOneRequest) (*schema.SystemOneResponse, error) {
						if !bytes.Contains(r.State, []byte(expectedImage)) {
							Fail(fmt.Sprint("image missing from native probe"))
						}
						answers := map[string]schema.SystemOneAnswer{}
						for key := range r.Questions {
							v := 0.0
							if key == "p1" {
								v = 1
							}
							answers[key] = schema.SystemOneAnswer{Type: "noul", Noul: &v}
						}
						return &schema.SystemOneResponse{Answers: answers}, nil
					})
				}
			}
			reached := false
			err := RouteModel(loader, app, nil, nil, extractor, router.SourceChat, deps)(func(echo.Context) error { reached = true; return nil })(c)
			if err != nil || !reached {
				Fail(fmt.Sprintf("dispatch: %v", err))
			}
			if len(req.Messages[0].StringImages) != 1 || req.Messages[0].StringImages[0] != expectedImage {
				Fail(fmt.Sprintf("images lost: %#v", req.Messages[0]))
			}
			after, _ := json.Marshal(req.Messages[0].Content)
			if string(before) != string(after) {
				Fail(fmt.Sprint("original content changed"))
			}
		}()
	}
})

var _ = It("TestRetryOrderedProbes", func() {
	for _, api := range []string{"openai", "anthropic"} {
		raw := `[{"type":"text","text":"first"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}},{"type":"text","text":"second"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AQ=="}}]`
		if api == "anthropic" {
			raw = `[{"type":"text","text":"first"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}},{"type":"text","text":"second"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQ=="}}]`
		}
		var untyped []any
		if err := json.Unmarshal([]byte(raw), &untyped); err != nil {
			Fail(fmt.Sprint(err))
		}
		var typed any
		if api == "openai" {
			var blocks []schema.Content
			Expect(json.Unmarshal([]byte(raw), &blocks)).To(Succeed())
			typed = blocks
		} else {
			var blocks []schema.AnthropicContentBlock
			Expect(json.Unmarshal([]byte(raw), &blocks)).To(Succeed())
			typed = blocks
		}
		for _, content := range []any{typed, untyped} {
			var p router.Probe
			if api == "openai" {
				p = OpenAIProbeFromRequest(&schema.OpenAIRequest{Messages: []schema.Message{{Role: "user", Content: content}}})
			} else {
				p, _ = AnthropicProbe(&schema.AnthropicRequest{Messages: []schema.AnthropicMessage{{Role: "user", Content: content}}})
			}
			if p.InputError != nil || p.Prompt != "first\nsecond\n" {
				Fail(fmt.Sprintf("%s: %#v", api, p))
			}
			images, err := systemone.CollectImages(&schema.SystemOneRequest{State: p.State})
			if err != nil || !reflect.DeepEqual(images, []string{"data:image/png;base64,AA==", "data:image/png;base64,AQ=="}) {
				Fail(fmt.Sprintf("%s order: %v %v", api, images, err))
			}
		}
	}
})

var _ = It("TestRetryAnthropicAndMarshalFallback", func() {
	for _, bad := range []bool{false, true} {
		By(fmt.Sprint(bad))
		func() {
			dir := GinkgoT().TempDir()
			cfg := newScoreRouterModel(dir, "smart-router")
			writeCandidate(dir, cfg.Router.Fallback)
			content := []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AA=="}}}
			if bad {
				content = append(content, map[string]any{"type": "text", "text": "keep", "unencodable": make(chan int)})
			}
			req := &schema.AnthropicRequest{Model: cfg.Name, Messages: []schema.AnthropicMessage{{Role: "user", Content: content}}}
			original := req.Messages[0].Content
			probe, _ := AnthropicProbe(req)
			if bad && probe.InputError == nil {
				Fail(fmt.Sprint("marshal error lost"))
			}
			app := &config.ApplicationConfig{Context: context.Background(), SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}}}
			c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), httptest.NewRecorder())
			c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, req)
			c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
			reached := false
			err := RouteModel(config.NewModelConfigLoader(dir), app, nil, nil, AnthropicProbe, router.SourceAnthropic, ClassifierDeps{Scorer: func(string) backend.Scorer { return &stubScorer{} }})(func(echo.Context) error { reached = true; return nil })(c)
			if err != nil || !reached || req.Model != cfg.Router.Fallback {
				Fail(fmt.Sprintf("fallback: %v", err))
			}
			if !reflect.DeepEqual(original, req.Messages[0].Content) {
				Fail(fmt.Sprint("payload changed"))
			}
		}()
	}
})
