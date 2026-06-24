package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The distributed controller pins a model's replicas to the revision of its
// persisted configuration. Inference requests only ever *establish* that
// revision, so a revision that varies per request permanently wedges the model:
// the first request's value is stored, and every later request carrying a
// different one is rejected with "stale model config revision".
var _ = Describe("Model config revision seen by inference requests", func() {
	var (
		app      *echo.Echo
		modelDir string
	)

	// revisionFor drives the real request pipeline (SetModelAndConfig ->
	// SetOpenAIRequest) and returns the config revision the handler is left
	// holding: the value core/backend.ModelOptions forwards to the model
	// router, and that the controller stores as the model's revision.
	revisionFor := func(body string) string {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK), "request pipeline rejected the request: %s", rec.Body.String())
		// An unstamped config would make every comparison below trivially true.
		Expect(rec.Body.String()).ToNot(BeEmpty(), "no config revision reached the handler")
		return rec.Body.String()
	}

	BeforeEach(func() {
		var err error
		modelDir, err = os.MkdirTemp("", "localai-revision-models-*")
		Expect(err).ToNot(HaveOccurred())

		Expect(os.WriteFile(
			filepath.Join(modelDir, "test-model.yaml"),
			// The mmproj makes this derive several usecase flags. A single-flag
			// model hides any instability in how that derived list is ordered.
			[]byte("name: test-model\nbackend: llama-cpp\ncontext_size: 4096\n"+
				"mmproj: llama-cpp/mmproj/test-model/mmproj.gguf\n"+
				"known_usecases:\n    - chat\n"),
			0o600,
		)).To(Succeed())

		ss := &system.SystemState{Model: system.Model{ModelsPath: modelDir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss

		mcl := config.NewModelConfigLoader(modelDir)
		ml := model.NewModelLoader(ss)
		re := NewRequestExtractor(mcl, ml, appConfig)

		app = echo.New()
		app.POST("/v1/chat/completions",
			func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				cfg, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
				Expect(ok).To(BeTrue())
				return c.String(http.StatusOK, cfg.PersistedConfigRevision())
			},
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		)
	})

	AfterEach(func() { Expect(os.RemoveAll(modelDir)).To(Succeed()) })

	It("is identical for requests that differ only in sampling parameters", func() {
		baseline := revisionFor(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)

		Expect(revisionFor(`{"model":"test-model","temperature":0.9,"messages":[{"role":"user","content":"hi"}]}`)).
			To(Equal(baseline), "temperature must not change the persisted config revision")
		Expect(revisionFor(`{"model":"test-model","top_p":0.5,"messages":[{"role":"user","content":"hi"}]}`)).
			To(Equal(baseline), "top_p must not change the persisted config revision")
		Expect(revisionFor(`{"model":"test-model","top_k":20,"messages":[{"role":"user","content":"hi"}]}`)).
			To(Equal(baseline), "top_k must not change the persisted config revision")
		Expect(revisionFor(`{"model":"test-model","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)).
			To(Equal(baseline), "max_tokens must not change the persisted config revision")
		Expect(revisionFor(`{"model":"test-model","stop":"STOP","messages":[{"role":"user","content":"hi"}]}`)).
			To(Equal(baseline), "stop words must not change the persisted config revision")
	})

	It("is identical for repeated requests carrying the same sampling parameters", func() {
		body := `{"model":"test-model","temperature":0.2,"stop":"END","messages":[{"role":"user","content":"hi"}]}`
		Expect(revisionFor(body)).To(Equal(revisionFor(body)))
	})

	// The controller compares the revision an inference request establishes
	// against the one model administration publishes when a YAML changes. If
	// the two paths hash different things, an edited model can never be routed
	// again, so they must agree on the same persisted configuration.
	It("matches the revision model administration computes for the same config", func() {
		ss := &system.SystemState{Model: system.Model{ModelsPath: modelDir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss

		admin := config.NewModelConfigLoader(modelDir)
		Expect(admin.LoadModelConfigsFromPath(modelDir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
		loaded, ok := admin.GetModelConfig("test-model")
		Expect(ok).To(BeTrue())
		adminRevision := loaded.PersistedConfigRevision()
		Expect(adminRevision).ToNot(BeEmpty())

		Expect(revisionFor(`{"model":"test-model","temperature":0.7,"messages":[{"role":"user","content":"hi"}]}`)).
			To(Equal(adminRevision))
	})
})
