package application

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

type applicationLoaderMaterializer struct {
	seen []modelartifacts.Spec
}

func (f *applicationLoaderMaterializer) Ensure(_ context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	f.seen = append(f.seen, spec)
	spec.Resolved = &modelartifacts.Resolved{
		Endpoint: "https://huggingface.co",
		Revision: "0123456789abcdef0123456789abcdef01234567",
		CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	return modelartifacts.Result{Spec: spec}, nil
}

var _ = Describe("application model artifact wiring", func() {
	It("injects the controller materializer into the model config loader", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(
			system.WithModelPath(modelsPath),
			system.WithBackendPath(GinkgoT().TempDir()),
		)
		Expect(err).NotTo(HaveOccurred())
		materializer := &applicationLoaderMaterializer{}
		app := newApplication(&config.ApplicationConfig{
			Context:                   context.Background(),
			SystemState:               state,
			ModelArtifactMaterializer: materializer,
		})
		Expect(os.WriteFile(filepath.Join(modelsPath, "managed.yaml"), []byte(`
name: managed
backend: transformers
artifacts:
  - source: {type: huggingface, repo: owner/repo}
parameters: {model: owner/repo}
`), 0644)).To(Succeed())
		Expect(app.ModelConfigLoader().LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		Expect(app.ModelConfigLoader().PreloadWithContext(context.Background(), modelsPath)).To(Succeed())
		Expect(materializer.seen).To(HaveLen(1))
	})
})
