package middleware_test

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The backend process of a model is keyed by the target's name (ModelID), and
// the backend rejects any request whose ModelIdentity (cfg.Model) differs from
// the one it was loaded with (pkg/grpc/server.go checkModelIdentity). Every
// name that reaches the same process must therefore carry the same cfg.Model.
// A target without parameters.model used to take cfg.Model from the requested
// name, so the target and each of its aliases or failover chains got a
// different identity for one process, and whichever loaded first locked the
// others out.
var _ = Describe("Model identity of names that share one backend process", func() {
	var (
		app      *echo.Echo
		modelDir string
		captured *config.ModelConfig
	)

	BeforeEach(func() {
		var err error
		modelDir, err = os.MkdirTemp("", "localai-alias-identity-*")
		Expect(err).ToNot(HaveOccurred())

		files := map[string]string{
			// A cloud-proxy style target: no parameters.model.
			"remote.yaml":       "name: remote\nbackend: cloud-proxy\n",
			"remote-a.yaml":     "name: remote-a\nalias: remote\n",
			"remote-b.yaml":     "name: remote-b\nalias: remote\nprofile:\n  system_prompt: persona\n",
			"remote-chain.yaml": "name: remote-chain\nfailover:\n  targets:\n    - model: remote\n    - model: other\n",
			"local.yaml":        "name: local\nbackend: llama-cpp\nparameters:\n  model: local.gguf\n",
			"local-alias.yaml":  "name: local-alias\nalias: local\n",
			"other.yaml":        "name: other\nbackend: cloud-proxy\n",
		}
		for name, body := range files {
			Expect(os.WriteFile(filepath.Join(modelDir, name), []byte(body), 0644)).To(Succeed())
		}

		ss := &system.SystemState{Model: system.Model{ModelsPath: modelDir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss
		mcl := config.NewModelConfigLoader(modelDir)
		Expect(mcl.LoadModelConfigsFromPath(modelDir)).To(Succeed())
		re := NewRequestExtractor(mcl, model.NewModelLoader(ss), appConfig)
		fm := failover.New(mcl)
		fm.Sync()
		re.SetFailoverManager(fm)

		captured = nil
		app = echo.New()
		app.POST("/v1/chat/completions",
			func(c echo.Context) error {
				captured, _ = c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
				return c.String(http.StatusOK, "ok")
			},
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
			func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					if err := re.SetOpenAIRequest(c); err != nil {
						return err
					}
					return next(c)
				}
			},
		)
	})

	AfterEach(func() {
		_ = os.RemoveAll(modelDir)
	})

	// served returns the loader key and the identity the backend sees for a
	// chat request to name.
	served := func(name string) (string, string) {
		rec := postJSON(app, "/v1/chat/completions",
			`{"model":"`+name+`","messages":[{"role":"user","content":"hi"}]}`)
		ExpectWithOffset(1, rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		ExpectWithOffset(1, captured).ToNot(BeNil())
		return captured.ModelID(), captured.Model
	}

	It("gives a target without parameters.model and its aliases one identity", func() {
		for _, name := range []string{"remote", "remote-a", "remote-b"} {
			key, identity := served(name)
			Expect(key).To(Equal("remote"), name)
			Expect(identity).To(Equal("remote"), name)
		}
	})

	It("gives a failover chain the identity of the target it resolved to", func() {
		key, identity := served("remote-chain")
		Expect(key).To(Equal("remote"))
		Expect(identity).To(Equal("remote"))
	})

	It("keeps parameters.model as the identity when the target sets it", func() {
		for _, name := range []string{"local", "local-alias"} {
			_, identity := served(name)
			Expect(identity).To(Equal("local.gguf"), name)
		}
	})

	It("still gives a different model its own identity", func() {
		_, remote := served("remote")
		_, other := served("other")
		Expect(other).To(Equal("other"))
		Expect(other).ToNot(Equal(remote))
	})
})
