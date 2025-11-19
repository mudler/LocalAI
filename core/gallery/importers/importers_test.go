package importers_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

		It("should discover and import using LlamaCPPImporter", func() {
			uri := "https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.Name).To(Equal("Qwen3-VL-2B-Instruct-GGUF"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Description).To(Equal("Imported from https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("mmproj: mmproj/mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: Qwen3VL-2B-Instruct-Q4_K_M.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(len(modelConfig.Files)).To(Equal(2), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("Qwen3VL-2B-Instruct-Q4_K_M.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF/resolve/main/Qwen3VL-2B-Instruct-Q4_K_M.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].SHA256).ToNot(BeEmpty(), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[1].Filename).To(Equal("mmproj/mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[1].URI).To(Equal("https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF/resolve/main/mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[1].SHA256).ToNot(BeEmpty(), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("should discover and import using LlamaCPPImporter", func() {
			uri := "https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF"
			preferences := json.RawMessage(`{ "quantizations": "Q8_0", "mmproj_quantizations": "f16" }`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.Name).To(Equal("Qwen3-VL-2B-Instruct-GGUF"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Description).To(Equal("Imported from https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("mmproj: mmproj/mmproj-Qwen3VL-2B-Instruct-F16.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: Qwen3VL-2B-Instruct-Q8_0.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(len(modelConfig.Files)).To(Equal(2), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("Qwen3VL-2B-Instruct-Q8_0.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF/resolve/main/Qwen3VL-2B-Instruct-Q8_0.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].SHA256).ToNot(BeEmpty(), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[1].Filename).To(Equal("mmproj/mmproj-Qwen3VL-2B-Instruct-F16.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[1].URI).To(Equal("https://huggingface.co/Qwen/Qwen3-VL-2B-Instruct-GGUF/resolve/main/mmproj-Qwen3VL-2B-Instruct-F16.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[1].SHA256).ToNot(BeEmpty(), fmt.Sprintf("Model config: %+v", modelConfig))
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

	Context("with local YAML config files", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "importers-test-*")
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("should read local YAML file with file:// prefix", func() {
			yamlContent := `name: test-model
backend: llama-cpp
description: Test model from local YAML
parameters:
  model: /path/to/model.gguf
  temperature: 0.7
`
			yamlFile := filepath.Join(tempDir, "test-model.yaml")
			err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
			Expect(err).ToNot(HaveOccurred())

			uri := "file://" + yamlFile
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("test-model"))
			Expect(modelConfig.Description).To(Equal("Test model from local YAML"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("name: test-model"))
		})

		It("should read local YAML file without file:// prefix (direct path)", func() {
			yamlContent := `name: direct-path-model
backend: mlx
description: Test model from direct path
parameters:
  model: /path/to/model.safetensors
`
			yamlFile := filepath.Join(tempDir, "direct-model.yaml")
			err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
			Expect(err).ToNot(HaveOccurred())

			uri := yamlFile
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("direct-path-model"))
			Expect(modelConfig.Description).To(Equal("Test model from direct path"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx"))
		})

		It("should read local YAML file with .yml extension", func() {
			yamlContent := `name: yml-extension-model
backend: transformers
description: Test model with .yml extension
parameters:
  model: /path/to/model
`
			yamlFile := filepath.Join(tempDir, "test-model.yml")
			err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
			Expect(err).ToNot(HaveOccurred())

			uri := "file://" + yamlFile
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("yml-extension-model"))
			Expect(modelConfig.Description).To(Equal("Test model with .yml extension"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: transformers"))
		})

		It("should ignore preferences when reading YAML files directly", func() {
			yamlContent := `name: yaml-model
backend: llama-cpp
description: Original description
parameters:
  model: /path/to/model.gguf
`
			yamlFile := filepath.Join(tempDir, "prefs-test.yaml")
			err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
			Expect(err).ToNot(HaveOccurred())

			uri := "file://" + yamlFile
			// Preferences should be ignored when reading YAML directly
			preferences := json.RawMessage(`{"name": "custom-name", "description": "Custom description", "backend": "mlx"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred())
			// Should use values from YAML file, not preferences
			Expect(modelConfig.Name).To(Equal("yaml-model"))
			Expect(modelConfig.Description).To(Equal("Original description"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"))
		})

		It("should return error when local YAML file doesn't exist", func() {
			nonExistentFile := filepath.Join(tempDir, "nonexistent.yaml")
			uri := "file://" + nonExistentFile
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).To(HaveOccurred())
			Expect(modelConfig.Name).To(BeEmpty())
		})

		It("should return error when YAML file is invalid/malformed", func() {
			invalidYaml := `name: invalid-model
backend: llama-cpp
invalid: yaml: content: [unclosed bracket
`
			yamlFile := filepath.Join(tempDir, "invalid.yaml")
			err := os.WriteFile(yamlFile, []byte(invalidYaml), 0644)
			Expect(err).ToNot(HaveOccurred())

			uri := "file://" + yamlFile
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).To(HaveOccurred())
			Expect(modelConfig.Name).To(BeEmpty())
		})
	})
})
