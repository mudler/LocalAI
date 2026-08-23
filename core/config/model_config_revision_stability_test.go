package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
)

// The distributed controller pins a model's replicas to its config revision and
// rejects any request carrying a different one. A revision that is not stable
// for one unchanged file on disk therefore wedges the model.
var _ = Describe("Model config revision stability", func() {
	// A chat model with an mmproj derives two usecase flags, FLAG_CHAT and
	// FLAG_VISION. syncKnownUsecasesFromString builds that list by ranging a
	// map, so an unstable order shows up with two or more flags and stays
	// hidden with one.
	const multiUsecaseModel = `backend: llama-cpp
context_size: 50000
known_usecases:
    - chat
mmproj: llama-cpp/mmproj/example/mmproj.gguf
name: example
options:
    - use_jinja:true
    - parallel:2
parameters:
    model: llama-cpp/models/example/example.gguf
template:
    use_tokenizer_template: true
`

	var (
		dir       string
		appConfig *config.ApplicationConfig
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "example.yaml"), []byte(multiUsecaseModel), 0o600)).To(Succeed())
		appConfig = config.NewApplicationConfig()
		appConfig.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
	})

	loadRevision := func() string {
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
		cfg, ok := loader.GetModelConfig("example")
		Expect(ok).To(BeTrue())
		revision, err := config.ModelConfigRevision(&cfg)
		Expect(err).ToNot(HaveOccurred())
		return revision
	}

	It("does not change when the same file is loaded repeatedly", func() {
		baseline := loadRevision()
		for i := 0; i < 20; i++ {
			Expect(loadRevision()).To(Equal(baseline), "revision changed between two loads of one unchanged file")
		}
	})

	It("orders the derived usecases deterministically", func() {
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
		cfg, ok := loader.GetModelConfig("example")
		Expect(ok).To(BeTrue())
		Expect(len(cfg.KnownUsecaseStrings)).To(BeNumerically(">=", 2), "fixture must derive several usecases to expose ordering")
		Expect(cfg.KnownUsecaseStrings).To(Equal([]string{"FLAG_CHAT", "FLAG_VISION"}))
	})

	// The request pipeline reloads the config through LoadModelConfigFileByName,
	// which applies SetDefaults a second time. That must not move the revision
	// away from the one model administration publishes from the loader map.
	It("survives the extra SetDefaults the request path applies", func() {
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
		stored, ok := loader.GetModelConfig("example")
		Expect(ok).To(BeTrue())
		adminRevision, err := config.ModelConfigRevision(&stored)
		Expect(err).ToNot(HaveOccurred())

		requestCfg, err := loader.LoadModelConfigFileByNameDefaultOptions("example", appConfig)
		Expect(err).ToNot(HaveOccurred())
		Expect(requestCfg.PersistedConfigRevision()).To(Equal(adminRevision))
	})
})
