package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The RouteModel middleware wires the score classifier into request
// rewriting. The classifier itself is covered in
// router/score_test.go — these specs pin the middleware-level
// behaviour: candidate matching against the active label set, the
// fallback path, and the depth-1 invariant.

var _ = Describe("RouteModel middleware (score classifier)", func() {
	var (
		modelDir  string
		appConfig *config.ApplicationConfig
		loader    *config.ModelConfigLoader
		store     *fakeDecisionStore
	)

	BeforeEach(func() {
		d, err := os.MkdirTemp("", "router-test-*")
		Expect(err).NotTo(HaveOccurred())
		modelDir = d
		appConfig = &config.ApplicationConfig{
			Context:     context.Background(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: modelDir}},
		}
		loader = config.NewModelConfigLoader(modelDir)
		store = &fakeDecisionStore{}
	})

	AfterEach(func() {
		_ = os.RemoveAll(modelDir)
	})

	It("routes to a candidate whose labels cover the active set", func() {
		// 3 policies, 2 candidates. Small model has [casual-chat],
		// bigger has [code-generation, math-reasoning, casual-chat].
		// A query that activates code-generation should fall to the
		// bigger candidate because it's the only one that covers it.
		routerCfg := newScoreRouterModel(modelDir, "smart-router")
		writeCandidate(modelDir, "small-model")
		writeCandidate(modelDir, "big-model")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -0.05, // dominant
			"casual-chat":     -3.0,
			"math-reasoning":  -4.0,
		}}
		rec, err := runRouter(loader, appConfig, store, routerCfg, openAIChat("debug my Go null pointer"), stubScorerFactory(s))
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("served:big-model"))
		Expect(store.records).To(HaveLen(1))
		Expect(store.records[0].ServedModel).To(Equal("big-model"))
		Expect(store.records[0].Label).To(ContainSubstring("code-generation"))
	})

	It("prefers the smaller candidate when both cover the active set", func() {
		// Both candidates list casual-chat. Admins order small →
		// big, so a casual-chat-only request must route to small.
		routerCfg := newScoreRouterModel(modelDir, "smart-router")
		writeCandidate(modelDir, "small-model")
		writeCandidate(modelDir, "big-model")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -5.0,
			"casual-chat":     -0.05, // dominant
			"math-reasoning":  -5.0,
		}}
		rec, err := runRouter(loader, appConfig, store, routerCfg, openAIChat("hi"), stubScorerFactory(s))
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Body.String()).To(Equal("served:small-model"))
	})

	It("falls back when no candidate covers the active label set", func() {
		// Only the bigger candidate covers math-reasoning. We
		// deliberately drop it from the candidates list so neither
		// matches; expect Fallback to fire.
		routerCfg := newScoreRouterModel(modelDir, "smart-router")
		// Remove the second candidate so coverage gap appears.
		routerCfg.Router.Candidates = routerCfg.Router.Candidates[:1]
		writeCandidate(modelDir, "small-model")
		writeCandidate(modelDir, "qwen3-0.6b")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -5.0,
			"casual-chat":     -5.0,
			"math-reasoning":  -0.05, // dominant — but no candidate has it
		}}
		rec, err := runRouter(loader, appConfig, store, routerCfg, openAIChat("3 apples cost $2.40"), stubScorerFactory(s))
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Body.String()).To(Equal("served:qwen3-0.6b"))
	})

	It("rejects candidates that reference unknown labels at build time", func() {
		routerCfg := newScoreRouterModel(modelDir, "smart-router")
		routerCfg.Router.Candidates = append(routerCfg.Router.Candidates, config.RouterCandidate{
			Model:  "broken",
			Labels: []string{"nonexistent-label"},
		})
		writeCandidate(modelDir, "small-model")
		writeCandidate(modelDir, "big-model")
		writeCandidate(modelDir, "broken")
		writeCandidate(modelDir, "qwen3-0.6b")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -0.05,
			"casual-chat":     -3.0,
			"math-reasoning":  -4.0,
		}}
		_, err := runRouter(loader, appConfig, store, routerCfg, openAIChat("debug something"), stubScorerFactory(s))
		// Build-time config bugs (here: a candidate referencing a
		// label not declared in policies) must surface to the client
		// — the previous silent-fallback behaviour hid the broken
		// config and left operators wondering why traces never showed
		// the classifier model running.
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown label"))
	})

	It("returns 500 when the candidate is itself a router (depth-1 invariant)", func() {
		// The candidate model is itself a router. We must reject
		// the dispatch — chained routers are deliberately
		// disallowed.
		routerCfg := newScoreRouterModel(modelDir, "smart-router")
		// Bend the test setup: replace one of the candidate-model
		// configs with a nested-router config.
		nestedRouter := newScoreRouterModel(modelDir, "small-model")
		Expect(os.WriteFile(filepath.Join(modelDir, "small-model.yaml"), []byte(toYAML(nestedRouter)), 0o644)).To(Succeed())
		writeCandidate(modelDir, "big-model")
		writeCandidate(modelDir, "qwen3-0.6b")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -5.0,
			"casual-chat":     -0.05,
			"math-reasoning":  -5.0,
		}}
		_, err := runRouter(loader, appConfig, store, routerCfg, openAIChat("hi"), stubScorerFactory(s))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("depth-1 invariant"))
	})
})

// Regression coverage for the rendered routing prompt — pins the
// guarantee that the routing system prompt (route listing, JSON
// output schema) actually reaches the classifier model. The first
// implementation of the template-aware renderer routed through
// EvaluateTemplateForPrompt, which only invokes the outer Chat
// template — and the gallery's outer Chat templates are
// `{{.Input -}}<|im_start|>assistant` shape, so .SystemPrompt was
// silently dropped. The fix routes through TemplateMessages, which
// renders each role through ChatMessage and joins the result into
// .Input. These specs would fail loudly if the renderer ever
// regresses back to bypassing per-role formatting.
var _ = Describe("RouteModel rendered classifier prompt", func() {
	var (
		modelDir  string
		appConfig *config.ApplicationConfig
		loader    *config.ModelConfigLoader
		store     *fakeDecisionStore
		eval      *templates.Evaluator
	)

	BeforeEach(func() {
		d, err := os.MkdirTemp("", "router-render-*")
		Expect(err).NotTo(HaveOccurred())
		modelDir = d
		appConfig = &config.ApplicationConfig{
			Context:     context.Background(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: modelDir}},
		}
		loader = config.NewModelConfigLoader(modelDir)
		store = &fakeDecisionStore{}
		eval = templates.NewEvaluator(modelDir)
	})

	AfterEach(func() {
		_ = os.RemoveAll(modelDir)
	})

	It("includes the routing system prompt in the rendered ChatML envelope", func() {
		// Mirrors the live arch-router-1.5b.yaml: chatml-style chat +
		// chat_message templates. This is the production-wired path.
		writeChatMLClassifierModel(modelDir, "arch-router")
		routerCfg := newScoreRouterModel(modelDir, "smart-router")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -0.05,
			"casual-chat":     -3.0,
			"math-reasoning":  -4.0,
		}}
		_, err := runRouterWithDeps(loader, appConfig, store, routerCfg,
			openAIChat("debug this null pointer"),
			ClassifierDeps{
				Scorer:      stubScorerFactory(s),
				ModelLookup: loaderLookup(loader, appConfig),
				Evaluator:   eval,
			})
		Expect(err).NotTo(HaveOccurred())

		// The routing system prompt must reach the scorer. Three
		// anchors: the route-listing block, one of the JSON-shaped
		// route entries (escapeJSONString preserves the description),
		// and the JSON output schema instruction.
		Expect(s.lastPrompt).To(ContainSubstring("<routes>"),
			"system prompt dropped: rendered prompt missing route-listing block. got: %q", s.lastPrompt)
		Expect(s.lastPrompt).To(ContainSubstring(`{"name": "code-generation"`),
			"system prompt dropped: rendered prompt missing route entries. got: %q", s.lastPrompt)
		Expect(s.lastPrompt).To(ContainSubstring(`{"route": "<name>"}`),
			"system prompt dropped: rendered prompt missing JSON output schema. got: %q", s.lastPrompt)

		// And the per-role envelope must be present (proves we went
		// through ChatMessage, not the SystemPrompt-only path).
		Expect(s.lastPrompt).To(ContainSubstring("<|im_start|>system"),
			"system role marker missing — ChatMessage template wasn't invoked")
		Expect(s.lastPrompt).To(ContainSubstring("<|im_start|>user"),
			"user role marker missing")
		// User probe makes it through the per-role template. The trailing
		// \n on the probe content is added by OpenAIProbeFromRequest;
		// preserved through ChatMessage rendering.
		Expect(s.lastPrompt).To(ContainSubstring("debug this null pointer"),
			"user probe missing from rendered prompt")
		// Outer Chat template must add the assistant-open marker so
		// the scorer's first predicted token is the start of the
		// candidate.
		Expect(s.lastPrompt).To(MatchRegexp(`<\|im_start\|>assistant\s*$`),
			"rendered prompt must end at assistant-open marker. got: %q", s.lastPrompt)
	})

	It("refuses to build the router when the classifier model has no chat_message template", func() {
		// Partial template config: only the outer Chat, no per-role piece.
		// The router renders the scoring prompt client-side from the
		// classifier model's own template, so a missing template is a hard
		// error rather than a silent fall back to a generic ChatML envelope
		// the model may not have been trained on.
		writePartialClassifierModel(modelDir, "arch-router")
		routerCfg := newScoreRouterModel(modelDir, "smart-router")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -0.05,
			"casual-chat":     -3.0,
			"math-reasoning":  -4.0,
		}}
		_, err := runRouterWithDeps(loader, appConfig, store, routerCfg,
			openAIChat("hello world"),
			ClassifierDeps{
				Scorer:      stubScorerFactory(s),
				ModelLookup: loaderLookup(loader, appConfig),
				Evaluator:   eval,
			})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no chat template"),
			"missing classifier template must surface as a clear config error. got: %v", err)
	})

	It("uses the classifier model's first stopword as the candidate suffix", func() {
		writeChatMLClassifierModel(modelDir, "arch-router")
		routerCfg := newScoreRouterModel(modelDir, "smart-router")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -0.05,
			"casual-chat":     -3.0,
			"math-reasoning":  -4.0,
		}}
		_, err := runRouterWithDeps(loader, appConfig, store, routerCfg,
			openAIChat("hi"),
			ClassifierDeps{
				Scorer:      stubScorerFactory(s),
				ModelLookup: loaderLookup(loader, appConfig),
				Evaluator:   eval,
			})
		Expect(err).NotTo(HaveOccurred())
		// arch-router YAML lists <|im_end|> first.
		for _, c := range s.lastCandidates {
			Expect(c).To(HaveSuffix("<|im_end|>"),
				"candidate must end with the classifier model's turn-end token. got: %q", c)
		}
	})

	It("picks the actual turn-end token when the stopwords list is misordered (Llama-3 style)", func() {
		// gallery/llama3-instruct.yaml et al. defensively list
		// <|im_end|> first even though the actual Llama-3 assistant
		// turn-end is <|eot_id|>. The naive "stopwords[0]" pick would
		// suffix candidates with <|im_end|> — a token Llama-3 never
		// emits at turn end. PickAssistantTurnEnd should scan the
		// chat_message template and recognise <|eot_id|> as the real
		// turn-end.
		writeLlama3StyleClassifierModel(modelDir, "arch-router")
		routerCfg := newScoreRouterModel(modelDir, "smart-router")

		s := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -0.05,
			"casual-chat":     -3.0,
			"math-reasoning":  -4.0,
		}}
		_, err := runRouterWithDeps(loader, appConfig, store, routerCfg,
			openAIChat("hi"),
			ClassifierDeps{
				Scorer:      stubScorerFactory(s),
				ModelLookup: loaderLookup(loader, appConfig),
				Evaluator:   eval,
			})
		Expect(err).NotTo(HaveOccurred())
		for _, c := range s.lastCandidates {
			Expect(c).To(HaveSuffix("<|eot_id|>"),
				"candidate must end with the Llama-3 turn-end token, not the misordered first stopword. got: %q", c)
		}
	})
})

// --- helpers ---

// stubScorer scores each candidate label according to a fixed
// label→log-prob map; per-token length is faked at 2 tokens so length
// normalisation is a no-op. Captures the prompt + candidate list of
// the last Score call so regression tests can pin the rendered prompt
// shape.
type stubScorer struct {
	labelToLogProb map[string]float64
	lastPrompt     string
	lastCandidates []string
}

func (s *stubScorer) Score(_ context.Context, prompt string, candidates []string) ([]backend.CandidateScore, error) {
	s.lastPrompt = prompt
	s.lastCandidates = append([]string(nil), candidates...)
	out := make([]backend.CandidateScore, len(candidates))
	for i, c := range candidates {
		// Match against the full `{"route": "<label>"}` envelope.
		// Naively substring-matching on `"<label>"` would let a label
		// that's a substring of another collide via Go's randomised
		// map iteration order — `"code"` would also match the
		// `"code-generation"` candidate.
		var lp float64
		for label, v := range s.labelToLogProb {
			if strings.Contains(c, `{"route": "`+label+`"}`) {
				lp = v
				break
			}
		}
		out[i] = backend.CandidateScore{
			LogProb:                 lp * 2,
			LengthNormalizedLogProb: lp,
			NumTokens:               2,
		}
	}
	return out, nil
}

func stubScorerFactory(s *stubScorer) ScorerFactory {
	return func(string) backend.Scorer { return s }
}

type fakeDecisionStore struct {
	records []router.DecisionRecord
}

func (f *fakeDecisionStore) Record(_ context.Context, r router.DecisionRecord) error {
	f.records = append(f.records, r)
	return nil
}

func (f *fakeDecisionStore) List(_ context.Context, _ router.DecisionListQuery) ([]router.DecisionRecord, error) {
	out := append([]router.DecisionRecord(nil), f.records...)
	return out, nil
}

func (f *fakeDecisionStore) Close() error                         { return nil }
func (f *fakeDecisionStore) Count(_ context.Context) (int, error) { return len(f.records), nil }

// newScoreRouterModel builds a smart-router config with 3 policies
// and 2 candidates (small with one label, bigger with all three).
// Admins are expected to order candidates small → large; the
// middleware picks the first whose labels are a superset of the
// active set.
func newScoreRouterModel(modelDir, name string) *config.ModelConfig {
	cfg := &config.ModelConfig{
		Name: name,
		Router: config.RouterConfig{
			Classifier:      "score",
			ClassifierModel: "arch-router",
			Fallback:        "qwen3-0.6b",
			Policies: []config.RouterPolicy{
				{Label: "code-generation", Description: "writing or debugging code"},
				{Label: "casual-chat", Description: "small talk"},
				{Label: "math-reasoning", Description: "arithmetic and word problems"},
			},
			Candidates: []config.RouterCandidate{
				{Model: "small-model", Labels: []string{"casual-chat"}},
				{Model: "big-model", Labels: []string{"code-generation", "casual-chat", "math-reasoning"}},
			},
		},
	}
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(toYAML(cfg)), 0o644)).To(Succeed())
	return cfg
}

func writeCandidate(modelDir, name string) {
	body := "name: " + name + "\nbackend: mock-backend\n"
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
}

func toYAML(cfg *config.ModelConfig) string {
	b, err := yaml.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

func openAIChat(content string) *schema.OpenAIRequest {
	req := &schema.OpenAIRequest{
		Messages: []schema.Message{
			{Role: "user", Content: content},
		},
	}
	req.Model = "smart-router"
	return req
}

func runRouter(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, store router.DecisionStore, routerCfg *config.ModelConfig, parsed any, scorerFactory ScorerFactory) (*httptest.ResponseRecorder, error) {
	return runRouterWithDeps(loader, appConfig, store, routerCfg, parsed, ClassifierDeps{Scorer: scorerFactory})
}

// runRouterWithDeps is runRouter's general form: lets the caller pass
// a fully-populated ClassifierDeps (ModelLookup, Evaluator, ...) so
// tests can exercise the template-renderer + stop-token derivation
// paths, not just the bare-scorer fast path.
func runRouterWithDeps(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, store router.DecisionStore, routerCfg *config.ModelConfig, parsed any, deps ClassifierDeps) (*httptest.ResponseRecorder, error) {
	mw := RouteModel(loader, appConfig, store, nil, OpenAIProbe, router.SourceChat, deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, routerCfg)
	c.Set(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, parsed)
	handler := mw(func(c echo.Context) error {
		served, _ := c.Get(ContextKeyServedModel).(string)
		return c.String(http.StatusOK, "served:"+served)
	})
	err := handler(c)
	return rec, err
}

// loaderLookup mirrors application.ModelConfigLookup — bridges the
// loader to the ModelConfigLookup signature ClassifierDeps wants.
func loaderLookup(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig) ModelConfigLookup {
	return func(name string) *config.ModelConfig {
		cfg, err := loader.LoadModelConfigFileByNameDefaultOptions(name, appConfig)
		if err != nil || cfg == nil {
			return nil
		}
		return cfg
	}
}

// writeChatMLClassifierModel writes a classifier model YAML that
// mirrors the live arch-router-1.5b.yaml shipped at
// volumes/models/arch-router-1.5b.yaml: ChatML chat + chat_message
// templates, score usecase, <|im_end|> first in stopwords.
func writeChatMLClassifierModel(modelDir, name string) {
	body := `name: ` + name + `
backend: llama-cpp
known_usecases:
  - score
stopwords:
  - <|im_end|>
  - <|endoftext|>
template:
  chat: |
    {{.Input -}}
    <|im_start|>assistant
  chat_message: |
    <|im_start|>{{ .RoleName }}
    {{- if .Content }}
    {{ .Content }}
    {{- end }}<|im_end|>
`
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
}

// writeLlama3StyleClassifierModel writes a classifier model mirroring
// gallery/llama3-instruct.yaml — stopwords defensively list <|im_end|>
// first even though the assistant turn-end is actually <|eot_id|>.
// Exercises PickAssistantTurnEnd's template scan: the right token is
// the one that appears in chat_message, not the one at position 0.
func writeLlama3StyleClassifierModel(modelDir, name string) {
	body := `name: ` + name + `
backend: llama-cpp
known_usecases:
  - score
stopwords:
  - <|im_end|>
  - <dummy32000>
  - "<|eot_id|>"
  - <|end_of_text|>
template:
  chat: |
    {{.Input }}
    <|start_header_id|>assistant<|end_header_id|>
  chat_message: |
    <|start_header_id|>{{ .RoleName }}<|end_header_id|>

    {{ .Content }}<|eot_id|>
`
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
}

// writePartialClassifierModel writes a classifier model that has the
// outer Chat template but no ChatMessage — exercises the
// NewTemplateRenderer "refuse partial templating" branch, which makes
// buildClassifier reject the router with a missing-template error.
func writePartialClassifierModel(modelDir, name string) {
	body := `name: ` + name + `
backend: llama-cpp
known_usecases:
  - score
stopwords:
  - <|im_end|>
template:
  chat: |
    {{.Input -}}
    <|im_start|>assistant
`
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
}
