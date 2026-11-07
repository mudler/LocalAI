package inproc

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("inproc.Client LoadModel", func() {
	var (
		ctx       context.Context
		tempDir   string
		cl        *config.ModelConfigLoader
		ml        *model.ModelLoader
		c         *Client
		seedModel func(name, body string)
	)

	BeforeEach(func() {
		ctx = context.Background()
		tempDir = GinkgoT().TempDir()
		systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).ToNot(HaveOccurred())
		appConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
		cl = config.NewModelConfigLoader(tempDir)
		ml = model.NewModelLoader(systemState) // no backends installed
		c = New(appConfig, systemState, cl, ml, nil)

		seedModel = func(name, body string) {
			Expect(os.WriteFile(filepath.Join(tempDir, name+".yaml"), []byte(body), 0o644)).To(Succeed())
			Expect(cl.LoadModelConfigsFromPath(tempDir)).To(Succeed())
		}
	})

	It("errors when the model loader is unavailable", func() {
		noLoader := New(c.AppConfig, c.SystemState, cl, nil, nil)
		_, err := noLoader.LoadModel(ctx, "anything")
		Expect(err).To(MatchError(ContainSubstring("model loader not available")))
	})

	It("loads a regular model through the model loader", func() {
		seedModel("solo", "name: solo\n")
		// No backend is installed in the test env, so the load itself fails — but
		// the call must exercise the single-model path and surface that error
		// rather than panicking or silently succeeding.
		loaded, err := c.LoadModel(ctx, "solo")
		Expect(err).To(HaveOccurred())
		Expect(loaded).To(BeEmpty())
	})

	It("expands a pipeline model into its sub-models", func() {
		seedModel("voicebot", "name: voicebot\npipeline:\n  vad: vad-m\n  llm: llm-m\n")
		seedModel("vad-m", "name: vad-m\n")
		seedModel("llm-m", "name: llm-m\n")

		loaded, err := c.LoadModel(ctx, "voicebot")
		// Sub-models can't load without backends, so the joined error names them
		// — proving the pipeline stub was expanded rather than loaded directly.
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("vad-m"))
		Expect(err.Error()).ToNot(ContainSubstring("voicebot"))
		Expect(loaded).To(BeEmpty())
	})
})
