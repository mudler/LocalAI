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
		Expect(cfg.PersistedConfigRevision()).ToNot(BeEmpty())
		return cfg.PersistedConfigRevision()
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
	// which applies SetDefaults a second time. The stamp is taken before those
	// defaults, so both the stored config and the one a request resolves carry
	// the same revision.
	It("survives the extra SetDefaults the request path applies", func() {
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
		stored, ok := loader.GetModelConfig("example")
		Expect(ok).To(BeTrue())
		Expect(stored.PersistedConfigRevision()).ToNot(BeEmpty())

		requestCfg, err := loader.LoadModelConfigFileByNameDefaultOptions("example", appConfig)
		Expect(err).ToNot(HaveOccurred())
		Expect(requestCfg.PersistedConfigRevision()).To(Equal(stored.PersistedConfigRevision()))
	})
})

// The revision must describe the configuration as persisted, and nothing else.
// SetDefaults folds in values that are not persisted config: the GGUF guess
// (which reads the model file and can fail on slow or remote storage), the
// hardware defaults, and app-level options like threads. Hashing after that
// made the revision a function of whether a multi-gigabyte file happened to
// parse, so one unchanged YAML produced two different revisions depending on
// the moment, and the controller rejected every request carrying the other one.
var _ = Describe("Model config revision independence from runtime defaults", func() {
	It("does not change when SetDefaults is applied", func() {
		dir := GinkgoT().TempDir()
		body := "backend: llama-cpp\ncontext_size: 50000\nknown_usecases:\n    - chat\n" +
			"mmproj: llama-cpp/mmproj/example/mmproj.gguf\nname: example\n" +
			"parameters:\n    model: llama-cpp/models/example/example.gguf\n"
		Expect(os.WriteFile(filepath.Join(dir, "example.yaml"), []byte(body), 0o600)).To(Succeed())

		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())

		stored, ok := loader.GetModelConfig("example")
		Expect(ok).To(BeTrue())
		before := stored.PersistedConfigRevision()
		Expect(before).ToNot(BeEmpty())

		// Applying defaults again is what the request path does.
		stored.SetDefaults(appConfig.ToConfigLoaderOptions()...)
		Expect(stored.PersistedConfigRevision()).To(Equal(before))

		resolved, err := loader.LoadModelConfigFileByNameDefaultOptions("example", appConfig)
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.PersistedConfigRevision()).To(Equal(before),
			"the request path must carry the same revision as the stored config")
	})

	It("does not change when app-level defaults differ", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "example.yaml"),
			[]byte("name: example\nbackend: llama-cpp\nparameters:\n    model: m.gguf\n"), 0o600)).To(Succeed())

		revWith := func(threads int, f16 bool) string {
			appConfig := config.NewApplicationConfig()
			appConfig.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
			appConfig.Threads = threads
			appConfig.F16 = f16
			loader := config.NewModelConfigLoader(dir)
			Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
			cfg, err := loader.LoadModelConfigFileByNameDefaultOptions("example", appConfig)
			Expect(err).ToNot(HaveOccurred())
			return cfg.PersistedConfigRevision()
		}

		Expect(revWith(8, false)).To(Equal(revWith(1, true)),
			"an operator changing threads must not make every model unroutable")
	})
})
