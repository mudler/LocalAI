package modeladmin

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
)

// A model's revision is published by administration and checked against on
// every inference request. Those were computed by different code, and each time
// they drifted the model became unroutable until someone deleted the row by
// hand: the request path resolves through the loader, while publishers hashed
// whatever ModelConfig they were holding, which by then had SetDefaults applied.
//
// There is now one resolver, ModelConfigLoader.RevisionFor, and the raw hash is
// unexported so a new publisher cannot reintroduce the split. This pins the
// property that mattered: whatever a publisher writes is what a request brings.
var _ = Describe("Published and requested revisions agree", func() {
	var (
		dir       string
		appConfig *config.ApplicationConfig
		loader    *config.ModelConfigLoader
	)

	// Several shapes, because the divergence only ever showed up on configs
	// rich enough for SetDefaults to change something: a model file to guess
	// from, several derived usecases, explicit options.
	models := map[string]string{
		"plain":       "name: plain\nbackend: llama-cpp\nparameters:\n    model: plain.gguf\n",
		"multimodal":  "name: multimodal\nbackend: llama-cpp\ncontext_size: 50000\nknown_usecases:\n    - chat\nmmproj: mm/mmproj.gguf\noptions:\n    - use_jinja:true\n    - parallel:2\nparameters:\n    model: mm/model.gguf\n",
		"auto-ctx":    "name: auto-ctx\nbackend: llama-cpp\ncontext_size: -1\nparameters:\n    model: auto.gguf\n",
		"no-backend":  "name: no-backend\nparameters:\n    model: bare.gguf\n",
		"with-thread": "name: with-thread\nbackend: llama-cpp\nthreads: 3\nparameters:\n    model: t.gguf\n",
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		for name, body := range models {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
		}
		appConfig = config.NewApplicationConfig()
		appConfig.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		appConfig.Threads = 8
		loader = config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir, appConfig.ToConfigLoaderOptions()...)).To(Succeed())
	})

	// requestRevision mirrors what core/backend.ModelOptions forwards to the
	// router: the stamp on the config the request pipeline resolved.
	requestRevision := func(name string) string {
		cfg, err := loader.LoadModelConfigFileByNameDefaultOptions(name, appConfig)
		Expect(err).ToNot(HaveOccurred())
		return cfg.PersistedConfigRevision()
	}

	It("resolves the same revision a request will carry, for every model shape", func() {
		for name := range models {
			published, err := loader.RevisionFor(name, appConfig)
			Expect(err).ToNot(HaveOccurred(), "model %s", name)
			Expect(published).To(Equal(requestRevision(name)), "model %s: publisher and request disagree", name)
		}
	})

	It("resolves the same revision through the path-based form", func() {
		for name := range models {
			byAppConfig, err := loader.RevisionFor(name, appConfig)
			Expect(err).ToNot(HaveOccurred())
			byPath, err := loader.RevisionForPath(name, dir, appConfig.ToConfigLoaderOptions()...)
			Expect(err).ToNot(HaveOccurred())
			Expect(byPath).To(Equal(byAppConfig), "model %s", name)
		}
	})

	It("does not move when the app-level defaults change", func() {
		before := map[string]string{}
		for name := range models {
			r, err := loader.RevisionFor(name, appConfig)
			Expect(err).ToNot(HaveOccurred())
			before[name] = r
		}

		other := config.NewApplicationConfig()
		other.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		other.Threads = 1
		other.F16 = true
		other.ContextSize = 4096
		fresh := config.NewModelConfigLoader(dir)
		Expect(fresh.LoadModelConfigsFromPath(dir, other.ToConfigLoaderOptions()...)).To(Succeed())

		for name := range models {
			r, err := fresh.RevisionFor(name, other)
			Expect(err).ToNot(HaveOccurred())
			Expect(r).To(Equal(before[name]),
				"model %s: changing an app-level setting must not make every model unroutable", name)
		}
	})
})
