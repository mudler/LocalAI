package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DiscoverModelConfig", func() {

	Context("With only a repository URI", func() {
		It("should discover and import using LlamaCPPImporter", func() {
			uri := "https://huggingface.co/mudler/LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.Name).To(Equal("LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Description).To(Equal("Imported from https://huggingface.co/mudler/LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(len(modelConfig.Files)).To(Equal(1), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("localai-functioncall-qwen2.5-7b-v0.5-q4_k_m.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://huggingface.co/mudler/LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF/resolve/main/localai-functioncall-qwen2.5-7b-v0.5-q4_k_m.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].SHA256).To(Equal("4e7b7fe1d54b881f1ef90799219dc6cc285d29db24f559c8998d1addb35713d4"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("with .gguf URI", func() {
		It("should discover and import using LlamaCPPImporter", func() {
			uri := "https://example.com/my-model.gguf"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-model.gguf"))
			Expect(modelConfig.Description).To(Equal("Imported from https://example.com/my-model.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"))
		})

		It("should use custom preferences when provided", func() {
			uri := "https://example.com/my-model.gguf"
			preferences := json.RawMessage(`{"name": "custom-name", "description": "Custom description"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("custom-name"))
			Expect(modelConfig.Description).To(Equal("Custom description"))
		})
	})

	Context("with mlx-community URI", func() {
		It("should discover and import using MLXImporter", func() {
			uri := "https://huggingface.co/mlx-community/test-model"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("test-model"))
			Expect(modelConfig.Description).To(Equal("Imported from https://huggingface.co/mlx-community/test-model"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx"))
		})

		It("should use custom preferences when provided", func() {
			uri := "https://huggingface.co/mlx-community/test-model"
			preferences := json.RawMessage(`{"name": "custom-mlx", "description": "Custom MLX description"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("custom-mlx"))
			Expect(modelConfig.Description).To(Equal("Custom MLX description"))
		})
	})

	Context("with backend preference", func() {
		It("should use llama-cpp backend when specified", func() {
			uri := "https://example.com/model"
			preferences := json.RawMessage(`{"backend": "llama-cpp"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"))
		})

		It("should use mlx backend when specified", func() {
			uri := "https://example.com/model"
			preferences := json.RawMessage(`{"backend": "mlx"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx"))
		})

		It("should use mlx-vlm backend when specified", func() {
			uri := "https://example.com/model"
			preferences := json.RawMessage(`{"backend": "mlx-vlm"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx-vlm"))
		})
	})

	Context("with HuggingFace URI formats", func() {
		It("should handle huggingface:// prefix", func() {
			uri := "huggingface://mlx-community/test-model"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("test-model"))
		})

		It("should handle hf:// prefix", func() {
			uri := "hf://mlx-community/test-model"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("test-model"))
		})

		It("should handle https://huggingface.co/ prefix", func() {
			uri := "https://huggingface.co/mlx-community/test-model"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("test-model"))
		})
	})

	Context("with invalid or non-matching URI", func() {
		It("should return error when no importer matches", func() {
			uri := "https://example.com/unknown-model.bin"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			// When no importer matches, the function returns empty config and error
			// The exact behavior depends on implementation, but typically an error is returned
			Expect(modelConfig.Name).To(BeEmpty())
			Expect(err).To(HaveOccurred())
		})
	})

	Context("with invalid JSON preferences", func() {
		It("should return error when JSON is invalid even if URI matches", func() {
			uri := "https://example.com/model.gguf"
			preferences := json.RawMessage(`invalid json`)

			// Even though Match() returns true for .gguf extension,
			// Import() will fail when trying to unmarshal invalid JSON preferences
			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).To(HaveOccurred())
			Expect(modelConfig.Name).To(BeEmpty())
		})
	})
})
