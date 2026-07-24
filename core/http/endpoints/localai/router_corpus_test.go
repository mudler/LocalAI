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
	"github.com/mudler/LocalAI/core/services/routing/corpus"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The corpus endpoints manage the knn classifier's labelled exemplar
// store. These specs pin the validation surface (knn-only, declared
// labels), the seed → stats → clear lifecycle, and the privacy
// contract: corpus texts never appear in any response body.

type corpusTestEmbedder struct{}

func (corpusTestEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text)), 1}, nil
}

// corpusTestStore implements only the narrow VectorStore interface —
// no batch/delete fast paths — so the specs also cover the manager's
// per-entry fallbacks.
type corpusTestStore struct {
	inserted int
}

func (s *corpusTestStore) Search(_ context.Context, _ []float32) (float64, []byte, bool, error) {
	return 0, nil, false, nil
}

func (s *corpusTestStore) SearchK(_ context.Context, _ []float32, _ int) ([]backend.Neighbor, error) {
	return nil, nil
}

func (s *corpusTestStore) Insert(_ context.Context, _ []float32, _ []byte) error {
	s.inserted++
	return nil
}

func writeKNNRouter(modelDir, name string) {
	cfg := &config.ModelConfig{
		Name: name,
		Router: config.RouterConfig{
			Classifier: "knn",
			Fallback:   "small-model",
			KNN:        &config.RouterKNNConfig{EmbeddingModel: "embed-model"},
			Policies: []config.RouterPolicy{
				{Label: "code-generation", Description: "writing or debugging code"},
				{Label: "casual-chat", Description: "small talk"},
			},
			Candidates: []config.RouterCandidate{
				{Model: "small-model", Labels: []string{"casual-chat"}},
				{Model: "big-model", Labels: []string{"code-generation", "casual-chat"}},
			},
		},
	}
	b, err := yaml.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(modelDir, name+".yaml"), []byte(b), 0o644)).To(Succeed())
}

var _ = Describe("Router corpus endpoints", func() {
	var (
		modelDir  string
		corpusDir string
		appConfig *config.ApplicationConfig
		loader    *config.ModelConfigLoader
		mgr       *corpus.Manager
		store     *corpusTestStore
		e         *echo.Echo
	)

	const seededText = "please debug this stack trace for me"

	BeforeEach(func() {
		d, err := os.MkdirTemp("", "router-corpus-ep-*")
		Expect(err).NotTo(HaveOccurred())
		modelDir = d
		corpusDir = filepath.Join(d, "corpus")
		appConfig = &config.ApplicationConfig{
			Context:     context.Background(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: modelDir}},
		}
		loader = config.NewModelConfigLoader(modelDir)
		mgr = corpus.NewManager(corpusDir)
		store = &corpusTestStore{}

		deps := middleware.ClassifierDeps{
			Embedder:            func(string) backend.Embedder { return corpusTestEmbedder{} },
			EmbedderFingerprint: func(string) (string, error) { return "test-embedding-fingerprint", nil },
			VectorStore:         func(string) backend.VectorStore { return store },
		}
		e = echo.New()
		e.POST("/api/router/:name/corpus", localai.RouterCorpusAddEndpoint(loader, appConfig, mgr, deps))
		e.GET("/api/router/:name/corpus/stats", localai.RouterCorpusStatsEndpoint(loader, appConfig, mgr))
		e.DELETE("/api/router/:name/corpus", localai.RouterCorpusClearEndpoint(loader, appConfig, mgr, deps))
	})

	AfterEach(func() {
		_ = os.RemoveAll(modelDir)
	})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	seedBody := `{"entries":[
		{"text":"` + seededText + `","labels":["code-generation"]},
		{"text":"how is your day going","labels":["casual-chat"]}
	]}`

	It("returns 404 for an unknown router", func() {
		rec := do(http.MethodPost, "/api/router/nope/corpus", seedBody)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("returns 400 for a router without a knn block", func() {
		writeScoreRouter(modelDir, "score-router")
		rec := do(http.MethodPost, "/api/router/score-router/corpus", seedBody)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("router.knn"))
	})

	It("rejects entries with undeclared labels", func() {
		writeKNNRouter(modelDir, "knn-router")
		rec := do(http.MethodPost, "/api/router/knn-router/corpus",
			`{"entries":[{"text":"hello","labels":["not-a-policy"]}]}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("not declared"))
	})

	It("rejects duplicate labels before they can inflate KNN votes", func() {
		writeKNNRouter(modelDir, "knn-router")
		rec := do(http.MethodPost, "/api/router/knn-router/corpus",
			`{"entries":[{"text":"hello","labels":["casual-chat","casual-chat"]}]}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("duplicate label"))
	})

	It("rejects a score router that merely has a stray knn block", func() {
		writeScoreRouter(modelDir, "score-router")
		path := filepath.Join(modelDir, "score-router.yaml")
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var cfg config.ModelConfig
		Expect(yaml.Unmarshal(raw, &cfg)).To(Succeed())
		cfg.Router.KNN = &config.RouterKNNConfig{EmbeddingModel: "embed-model"}
		raw, err = yaml.Marshal(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, raw, 0o644)).To(Succeed())

		rec := do(http.MethodPost, "/api/router/score-router/corpus", seedBody)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("classifier: knn"))
	})

	It("rejects an empty entries list", func() {
		writeKNNRouter(modelDir, "knn-router")
		rec := do(http.MethodPost, "/api/router/knn-router/corpus", `{"entries":[]}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("seeds, reports counts only, and clears", func() {
		writeKNNRouter(modelDir, "knn-router")

		rec := do(http.MethodPost, "/api/router/knn-router/corpus", seedBody)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var addResp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &addResp)).To(Succeed())
		Expect(addResp["added"]).To(BeNumerically("==", 2))
		Expect(addResp["total"]).To(BeNumerically("==", 2))
		Expect(store.inserted).To(Equal(2))

		// The seed response must not echo the entry texts back.
		Expect(rec.Body.String()).NotTo(ContainSubstring(seededText))

		rec = do(http.MethodGet, "/api/router/knn-router/corpus/stats", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var stats map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &stats)).To(Succeed())
		Expect(stats["total"]).To(BeNumerically("==", 2))
		Expect(stats["label_counts"]).To(HaveKeyWithValue("code-generation", BeNumerically("==", 1)))
		Expect(stats["embedding_model"]).To(Equal("embed-model"))
		// Privacy contract: the inspection surface never returns texts.
		Expect(rec.Body.String()).NotTo(ContainSubstring(seededText))

		rec = do(http.MethodDelete, "/api/router/knn-router/corpus", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var clr map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &clr)).To(Succeed())
		Expect(clr["cleared"]).To(BeNumerically("==", 2))

		rec = do(http.MethodGet, "/api/router/knn-router/corpus/stats", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var after map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &after)).To(Succeed())
		Expect(after["total"]).To(BeNumerically("==", 0))
	})

	It("skips duplicate texts on reseed", func() {
		writeKNNRouter(modelDir, "knn-router")
		Expect(do(http.MethodPost, "/api/router/knn-router/corpus", seedBody).Code).To(Equal(http.StatusOK))
		rec := do(http.MethodPost, "/api/router/knn-router/corpus", seedBody)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["added"]).To(BeNumerically("==", 0))
		Expect(resp["skipped"]).To(BeNumerically("==", 2))
		Expect(resp["total"]).To(BeNumerically("==", 2))
	})
})
