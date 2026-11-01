package localai

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the embedding model of the collections", func() {
	var (
		app    *application.Application
		models string
	)

	BeforeEach(func() {
		tmp := GinkgoT().TempDir()
		models = filepath.Join(tmp, "models")
		Expect(os.MkdirAll(models, 0o755)).To(Succeed())
		st, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(filepath.Join(tmp, "backends")))
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		app, err = application.New(config.WithContext(ctx), config.WithSystemState(st), config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true))
		Expect(err).ToNot(HaveOccurred())
	})

	It("is missing when the server has neither its configuration nor its file", func() {
		Expect(embeddingModelInstalled(app, "granite-embedding-107m-multilingual")).To(BeFalse())
		Expect(embeddingModelInstalled(app, "")).To(BeFalse())
		Expect(embeddingModelInstalled(app, "../outside")).To(BeFalse())
	})

	It("is installed when its configuration is loaded", func() {
		Expect(os.WriteFile(filepath.Join(models, "emb.yaml"), []byte("name: emb\nbackend: llama-cpp\nembeddings: true\nparameters:\n  model: emb.gguf\n"), 0o644)).To(Succeed())
		Expect(app.ModelConfigLoader().LoadModelConfigsFromPath(models)).To(Succeed())
		Expect(embeddingModelInstalled(app, "emb")).To(BeTrue())
	})

	It("is installed when a model file of that name is in the models folder", func() {
		Expect(os.WriteFile(filepath.Join(models, "plain.gguf"), []byte("x"), 0o644)).To(Succeed())
		Expect(embeddingModelInstalled(app, "plain.gguf")).To(BeTrue())
	})
})
