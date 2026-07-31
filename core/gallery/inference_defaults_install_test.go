package gallery_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("gallery inference defaults", func() {
	readPersistedConfig := func(modelsPath, name string) map[string]any {
		data, err := os.ReadFile(filepath.Join(modelsPath, name+".yaml"))
		Expect(err).NotTo(HaveOccurred())
		persisted := map[string]any{}
		Expect(yaml.Unmarshal(data, &persisted)).To(Succeed())
		return persisted
	}

	expectNestedDefaults := func(persisted map[string]any) {
		Expect(persisted).NotTo(HaveKey("temperature"))
		Expect(persisted).NotTo(HaveKey("top_p"))
		parameters, ok := persisted["parameters"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(parameters).To(HaveKeyWithValue("temperature", 0.7))
		Expect(parameters).To(HaveKeyWithValue("top_p", 0.42))
		Expect(parameters).To(HaveKeyWithValue("top_k", 20))
		Expect(parameters).To(HaveKeyWithValue("min_p", 0))
		Expect(parameters).To(HaveKeyWithValue("repeat_penalty", 1))
		Expect(parameters).To(HaveKeyWithValue("presence_penalty", 1.5))
	}

	It("persists defaults under parameters after artifact binding", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		resolved := modelartifacts.Spec{
			Name: "model", Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/qwen3.5-model", Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		}
		fake := &fakeArtifactMaterializer{result: modelartifacts.Result{Spec: resolved}}
		definition := &gallery.ModelConfig{Name: "qwen3.5-artifact", ConfigFile: `
backend: transformers
artifacts:
  - name: model
    target: model
    source: {type: huggingface, repo: owner/qwen3.5-model}
parameters:
  model: owner/qwen3.5-model
  top_p: 0.42
`}

		_, err = gallery.InstallModel(context.Background(), state, "", definition, nil, nil, false,
			gallery.WithArtifactMaterializer(fake))
		Expect(err).NotTo(HaveOccurred())
		expectNestedDefaults(readPersistedConfig(modelsPath, definition.Name))
	})

	It("persists defaults under parameters when the entry declares files", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(modelsPath, "weights.gguf"), []byte("weights"), 0644)).To(Succeed())
		definition := &gallery.ModelConfig{
			Name: "qwen3.5-files",
			ConfigFile: `
backend: llama-cpp
parameters:
  model: weights.gguf
  top_p: 0.42
`,
			Files: []gallery.File{{Filename: "weights.gguf", URI: "https://example.com/weights.gguf"}},
		}

		_, err = gallery.InstallModel(context.Background(), state, "", definition, nil, nil, false)
		Expect(err).NotTo(HaveOccurred())
		expectNestedDefaults(readPersistedConfig(modelsPath, definition.Name))
	})
})
