package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression: the router-facing factories used to capture
// *config.ModelConfig at construction. A gallery install that raced
// startup left a stub (Backend="") bound for the lifetime of the
// classifier registry's cache entry, bypassing the user's `backend:`
// config. These specs pin the lazy re-resolve.
var _ = Describe("router_factories lazy config resolution", func() {
	var (
		tmpDir string
		app    *Application
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "router-factories-*")
		Expect(err).NotTo(HaveOccurred())

		appCfg := &config.ApplicationConfig{
			Context:     context.Background(),
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: tmpDir}},
		}
		app = &Application{
			backendLoader:     config.NewModelConfigLoader(tmpDir),
			modelLoader:       model.NewModelLoader(appCfg.SystemState),
			applicationConfig: appCfg,
		}
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)
	})

	// writeCfg seeds both the on-disk YAML and the in-memory cache —
	// removing only the cache would fall through to file-read.
	writeCfg := func(name, backend string) {
		yaml := "name: " + name + "\nbackend: " + backend + "\nparameters:\n  model: " + name + ".bin\n"
		Expect(os.WriteFile(filepath.Join(tmpDir, name+".yaml"), []byte(yaml), 0644)).To(Succeed())
		Expect(app.backendLoader.LoadModelConfigsFromPath(tmpDir)).To(Succeed())
		cfg, ok := app.backendLoader.GetModelConfig(name)
		Expect(ok).To(BeTrue(), "config must be loaded before the spec runs")
		Expect(cfg.Backend).To(Equal(backend))
	}

	// removeCfg purges both the cache and the YAML so LoadModelConfigFileByName
	// returns the empty-stub case and adapterConfig returns nil.
	removeCfg := func(name string) {
		app.backendLoader.RemoveModelConfig(name)
		Expect(os.Remove(filepath.Join(tmpDir, name+".yaml"))).To(Succeed())
	}

	Context("Embedder", func() {
		It("returns nil at construction for an unknown model", func() {
			Expect(app.Embedder("missing")).To(BeNil())
		})

		It("re-resolves the model config on each Embed call", func() {
			writeCfg("emb-test", "llama-cpp")
			emb := app.Embedder("emb-test")
			Expect(emb).NotTo(BeNil())

			// The factory must hold the NAME, not a captured config —
			// otherwise stale captures survive cache invalidation.
			lazy, ok := emb.(*lazyEmbedder)
			Expect(ok).To(BeTrue(), "Embedder must return *lazyEmbedder")
			Expect(lazy.modelName).To(Equal("emb-test"))

			// Mutate the cached config. A lazy implementation sees the
			// update on the next adapterConfig call; a captured-at-
			// construction implementation would still see "llama-cpp".
			app.backendLoader.UpdateModelConfig("emb-test", func(c *config.ModelConfig) {
				c.Backend = "rerankers"
			})
			Expect(lazy.app.adapterConfig("emb-test").Backend).To(Equal("rerankers"))

			// Remove the config entirely → Embed must surface the disappearance.
			removeCfg("emb-test")
			_, err := emb.Embed(context.Background(), "anything")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no longer available"))
		})
	})

	Context("Scorer", func() {
		It("returns nil at construction for an unknown model", func() {
			Expect(app.Scorer("missing")).To(BeNil())
		})

		It("re-resolves the model config on each Score call", func() {
			writeCfg("score-test", "llama-cpp")
			sc := app.Scorer("score-test")
			Expect(sc).NotTo(BeNil())

			lazy, ok := sc.(*lazyScorer)
			Expect(ok).To(BeTrue(), "Scorer must return *lazyScorer")
			Expect(lazy.modelName).To(Equal("score-test"))

			removeCfg("score-test")
			_, err := sc.Score(context.Background(), "prompt", []string{"a"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no longer available"))
		})
	})

	Context("Reranker", func() {
		It("returns nil at construction for an unknown model", func() {
			Expect(app.Reranker("missing")).To(BeNil())
		})

		It("re-resolves the model config on each Rerank call", func() {
			writeCfg("rerank-test", "rerankers")
			rr := app.Reranker("rerank-test")
			Expect(rr).NotTo(BeNil())

			lazy, ok := rr.(*lazyReranker)
			Expect(ok).To(BeTrue(), "Reranker must return *lazyReranker")
			Expect(lazy.modelName).To(Equal("rerank-test"))

			removeCfg("rerank-test")
			_, err := rr.Rerank(context.Background(), "q", []string{"d"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no longer available"))
		})
	})

	Context("TokenCounter", func() {
		It("returns nil at construction for an unknown model", func() {
			Expect(app.TokenCounter("missing")).To(BeNil())
		})

		It("re-resolves the model config on each call", func() {
			writeCfg("tok-test", "llama-cpp")
			tc := app.TokenCounter("tok-test")
			Expect(tc).NotTo(BeNil())

			removeCfg("tok-test")
			_, err := tc("anything")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no longer available"))
		})
	})
})
