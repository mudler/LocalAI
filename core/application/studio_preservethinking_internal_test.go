package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("keeping the reasoning of earlier chat turns", func() {
	var (
		dir string
		app *Application
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		app = &Application{
			backendLoader:     config.NewModelConfigLoader(dir),
			applicationConfig: &config.ApplicationConfig{Context: context.Background()},
		}
	})

	load := func(name, yaml string) {
		Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0o644)).To(Succeed())
		Expect(app.backendLoader.LoadModelConfigsFromPath(dir)).To(Succeed())
	}

	It("is asked for a local model by default", func() {
		load("plain", "name: plain\nbackend: llama-cpp\nparameters:\n  model: plain.gguf\n")
		Expect(app.studioPreserveThinking("plain")).To(BeTrue())
	})

	It("follows the model's chat_template_kwargs", func() {
		load("off", "name: off\nbackend: llama-cpp\nparameters:\n  model: off.gguf\nchat_template_kwargs:\n  preserve_thinking: false\n")
		Expect(app.studioPreserveThinking("off")).To(BeFalse())
	})

	It("is not asked for an unknown model", func() {
		Expect(app.studioPreserveThinking("unknown")).To(BeFalse())
	})
})
