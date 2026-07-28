package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// RouterDecideEndpoint is the programmatic decision oracle that
// external routers call to get LocalAI's classifier opinion without
// committing LocalAI to handle the request. These specs pin the
// validation surface and the happy-path / fallback / depth-1
// behaviours; the classifier itself is covered in
// core/services/routing/router/score_test.go and the in-band
// middleware is covered in core/http/middleware/route_model_test.go.

var _ = Describe("RouterDecideEndpoint", func() {
	var (
		modelDir  string
		appConfig *config.ApplicationConfig
		loader    *config.ModelConfigLoader
	)

	BeforeEach(func() {
		d, err := os.MkdirTemp("", "router-decide-test-*")
		Expect(err).NotTo(HaveOccurred())
		modelDir = d
		appConfig = &config.ApplicationConfig{
			Context:     context.Background(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: modelDir}},
		}
		loader = config.NewModelConfigLoader(modelDir)
	})

	AfterEach(func() {
		_ = os.RemoveAll(modelDir)
	})

	It("rejects requests with no router field", func() {
		rec, _ := invokeDecide(loader, appConfig, deps(nil), `{"input":"hello"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("router is required"))
	})

	It("rejects requests with no input field", func() {
		rec, _ := invokeDecide(loader, appConfig, deps(nil), `{"router":"smart-router"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("input is required"))
	})

	It("returns 404 for an unknown router model", func() {
		rec, _ := invokeDecide(loader, appConfig, deps(nil), `{"router":"missing","input":"hello"}`)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(rec.Body.String()).To(ContainSubstring("router model not found"))
	})

	It("returns 400 when the named model has no router block", func() {
		writeBareModel(modelDir, "plain-model")
		rec, _ := invokeDecide(loader, appConfig, deps(nil), `{"router":"plain-model","input":"hello"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("is not a router"))
	})

	It("returns 503 when the classifier can't be built (no scorer wired)", func() {
		writeScoreRouter(modelDir, "smart-router")
		writeBareModel(modelDir, "small-model")
		writeBareModel(modelDir, "big-model")
		// deps(nil) provides no scorer — buildClassifier returns an
		// error and the handler maps that to 503.
		rec, _ := invokeDecide(loader, appConfig, deps(nil), `{"router":"smart-router","input":"hello"}`)
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(rec.Body.String()).To(ContainSubstring("classifier unavailable"))
	})

	It("returns the picked candidate when one covers the active labels", func() {
		writeScoreRouter(modelDir, "smart-router")
		writeBareModel(modelDir, "small-model")
		writeBareModel(modelDir, "big-model")
		scorer := &stubScorer{labelToLogProb: map[string]float64{
			"code-generation": -0.05, // dominant
			"casual-chat":     -3.0,
			"math-reasoning":  -4.0,
		}}
		rec, body := invokeDecide(loader, appConfig, deps(scorer), `{"router":"smart-router","input":"debug my Go null pointer"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(body.Candidate).To(Equal("big-model"))
		Expect(body.Fallback).To(BeFalse())
		Expect(body.Labels).To(ContainElement("code-generation"))
		Expect(body.Classifier).To(Equal(router.ClassifierScore))
		Expect(body.Score).To(BeNumerically(">", 0))
	})

	It("returns the fallback when no candidate covers the active labels", func() {
		// The router declares a label `math-reasoning` but no
		// candidate carries it — only small=[casual-chat] and
		// big=[code-generation, casual-chat]. A classifier output of
		// "math-reasoning" forces the fallback path.
		writeRouterNoFallbackCover(modelDir, "smart-router")
		writeBareModel(modelDir, "small-model")
		writeBareModel(modelDir, "big-model")
		writeBareModel(modelDir, "fallback-model")
		scorer := &stubScorer{labelToLogProb: map[string]float64{
			"math-reasoning":  -0.05,
			"code-generation": -3.0,
			"casual-chat":     -4.0,
		}}
		rec, body := invokeDecide(loader, appConfig, deps(scorer), `{"router":"smart-router","input":"3 apples cost $2.40"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(body.Candidate).To(Equal("fallback-model"))
		Expect(body.Fallback).To(BeTrue())
		Expect(body.Labels).To(ContainElement("math-reasoning"))
	})
})

// stubScorer mirrors the one in core/http/middleware/route_model_test.go.
// Duplicated rather than exported because Go test helpers don't cross
// _test.go package boundaries and exporting test-only types would
// pollute the production surface.
type stubScorer struct {
	labelToLogProb map[string]float64
}

func (s *stubScorer) Score(_ context.Context, _ string, candidates []string) ([]backend.CandidateScore, error) {
	out := make([]backend.CandidateScore, len(candidates))
	for i, c := range candidates {
		// Candidate is the Arch-Router JSON envelope
		// `{"route": "<label>"}<stop>`; match against the full
		// envelope so overlapping labels (e.g. `code` vs
		// `code-generation`) can't collide under Go's randomised map
		// iteration. Without this the lookup misses on every
		// candidate and softmax flattens, making assertions pass for
		// accidental reasons.
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

// deps wires a ClassifierDeps with a fresh registry and (optionally) a
// stub scorer. Nil scorer is used to exercise the unavailable path.
func deps(s *stubScorer) middleware.ClassifierDeps {
	var scorer middleware.ScorerFactory
	if s != nil {
		scorer = func(string) backend.Scorer { return s }
	}
	return middleware.ClassifierDeps{
		Scorer:   scorer,
		Registry: router.NewRegistry(),
	}
}

func invokeDecide(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, d middleware.ClassifierDeps, body string) (*httptest.ResponseRecorder, schema.RouterDecideResponse) {
	// Route through echo's mux so the default HTTPErrorHandler
	// serialises echo.HTTPError into the response body. Calling the
	// handler directly with a fresh Context skips that step and
	// leaves the recorder empty on errors.
	e := echo.New()
	e.POST("/api/router/decide", localai.RouterDecideEndpoint(loader, appConfig, d))
	req := httptest.NewRequest(http.MethodPost, "/api/router/decide", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var parsed schema.RouterDecideResponse
	if rec.Code == http.StatusOK {
		Expect(json.Unmarshal(rec.Body.Bytes(), &parsed)).To(Succeed())
	}
	return rec, parsed
}

func writeScoreRouter(modelDir, name string) {
	cfg := &config.ModelConfig{
		Name: name,
		Router: config.RouterConfig{
			Classifier:      "score",
			ClassifierModel: "arch-router",
			Fallback:        "small-model",
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
	b, err := yaml.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), b, 0o644)).To(Succeed())
}

// writeRouterNoFallbackCover declares math-reasoning as a policy but
// has no candidate covering it. Combined with Fallback=fallback-model,
// a math-reasoning classification forces the fallback branch.
func writeRouterNoFallbackCover(modelDir, name string) {
	cfg := &config.ModelConfig{
		Name: name,
		Router: config.RouterConfig{
			Classifier:      "score",
			ClassifierModel: "arch-router",
			Fallback:        "fallback-model",
			Policies: []config.RouterPolicy{
				{Label: "code-generation", Description: "writing or debugging code"},
				{Label: "casual-chat", Description: "small talk"},
				{Label: "math-reasoning", Description: "arithmetic and word problems"},
			},
			Candidates: []config.RouterCandidate{
				{Model: "small-model", Labels: []string{"casual-chat"}},
				{Model: "big-model", Labels: []string{"code-generation", "casual-chat"}},
			},
		},
	}
	b, err := yaml.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), b, 0o644)).To(Succeed())
}

func writeBareModel(modelDir, name string) {
	body := "name: " + name + "\nbackend: mock-backend\n"
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
}
