package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the step limit of a chat turn", func() {
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

	It("is 40 when nothing sets it", func() {
		Expect(app.studioAgentOptions("unknown").Iterations).To(Equal(config.DefaultChatsMaxIterations))
	})

	It("follows chats_max_iterations", func() {
		app.applicationConfig.ChatsMaxIterations = 75
		load("plain", "name: plain\nbackend: llama-cpp\nparameters:\n  model: plain.gguf\n")
		Expect(app.studioAgentOptions("plain").Iterations).To(Equal(75))
		Expect(app.studioAgentOptions("unknown").Iterations).To(Equal(75))
	})

	It("uses the model's agent.max_iterations when the model sets it", func() {
		app.applicationConfig.ChatsMaxIterations = 75
		load("capped", "name: capped\nbackend: llama-cpp\nparameters:\n  model: capped.gguf\nagent:\n  max_iterations: 12\n  max_attempts: 3\n")
		opts := app.studioAgentOptions("capped")
		Expect(opts.Iterations).To(Equal(12))
		Expect(opts.MaxAttempts).To(Equal(3))
	})
})
