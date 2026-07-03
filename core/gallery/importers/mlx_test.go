package importers_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MLXImporter", func() {
	var importer *importers.MLXImporter

	BeforeEach(func() {
		importer = &importers.MLXImporter{}
	})

	Context("Match", func() {
		It("should match when URI contains mlx-community/", func() {
			details := importers.Details{
				URI: "https://huggingface.co/mlx-community/test-model",
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when backend preference is mlx", func() {
			preferences := json.RawMessage(`{"backend": "mlx"}`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when backend preference is mlx-vlm", func() {
			preferences := json.RawMessage(`{"backend": "mlx-vlm"}`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should not match when URI does not contain mlx-community/ and no backend preference", func() {
			details := importers.Details{
				URI: "https://huggingface.co/other-org/test-model",
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})

		It("should not match when backend preference is different", func() {
			preferences := json.RawMessage(`{"backend": "llama-cpp"}`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})

		It("should return false when JSON preferences are invalid", func() {
			preferences := json.RawMessage(`invalid json`)
			details := importers.Details{
				URI:         "https://huggingface.co/mlx-community/test-model",
				Preferences: preferences,
			}

			// Invalid JSON causes Match to return false early
			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("should import model config with default name and description", func() {
			details := importers.Details{
				URI: "https://huggingface.co/mlx-community/test-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("test-model"))
			Expect(modelConfig.Description).To(Equal("Imported from https://huggingface.co/mlx-community/test-model"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: https://huggingface.co/mlx-community/test-model"))
		})

		It("should import model config with custom name and description from preferences", func() {
			preferences := json.RawMessage(`{"name": "custom-mlx-model", "description": "Custom MLX description"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/mlx-community/test-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("custom-mlx-model"))
			Expect(modelConfig.Description).To(Equal("Custom MLX description"))
		})

		It("should use custom backend from preferences", func() {
			preferences := json.RawMessage(`{"backend": "mlx-vlm"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/mlx-community/test-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx-vlm"))
		})

		It("should auto-route vision-language models to the mlx-vlm backend", func() {
			// gemma-4 E4B and similar VLMs declare pipeline_tag
			// "image-text-to-text" on HuggingFace. The text-only mlx-lm
			// tokenizer does not carry their processor chat template, so
			// routing them through the plain mlx backend produces degenerate
			// looping output (issue #10269). They must go to mlx-vlm.
			details := importers.Details{
				URI: "https://huggingface.co/mlx-community/gemma-4-E4B-it-qat-4bit",
				HuggingFace: &hfapi.ModelDetails{
					ModelID:     "mlx-community/gemma-4-E4B-it-qat-4bit",
					PipelineTag: "image-text-to-text",
				},
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx-vlm"))
		})

		It("should keep text-only models on the plain mlx backend", func() {
			details := importers.Details{
				URI: "https://huggingface.co/mlx-community/Llama-3.2-1B-Instruct-4bit",
				HuggingFace: &hfapi.ModelDetails{
					ModelID:     "mlx-community/Llama-3.2-1B-Instruct-4bit",
					PipelineTag: "text-generation",
				},
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx"))
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("backend: mlx-vlm"))
		})

		It("should honor an explicit backend preference even for a VLM", func() {
			preferences := json.RawMessage(`{"backend": "mlx"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/mlx-community/gemma-4-E4B-it-qat-4bit",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID:     "mlx-community/gemma-4-E4B-it-qat-4bit",
					PipelineTag: "image-text-to-text",
				},
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: mlx"))
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("backend: mlx-vlm"))
		})

		It("should handle invalid JSON preferences", func() {
			preferences := json.RawMessage(`invalid json`)
			details := importers.Details{
				URI:         "https://huggingface.co/mlx-community/test-model",
				Preferences: preferences,
			}

			_, err := importer.Import(details)
			Expect(err).To(HaveOccurred())
		})

		It("should extract filename correctly from URI with path", func() {
			details := importers.Details{
				URI: "https://huggingface.co/mlx-community/path/to/model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("model"))
		})

		It("should emit a bare filesystem path for a file:// local import", func() {
			// Regression for #7461: a model imported from a local directory
			// (e.g. LM Studio's store) must not carry the file:// scheme into
			// the model field — mlx-lm rejects it as an invalid repo id.
			preferences := json.RawMessage(`{"backend": "mlx"}`)
			details := importers.Details{
				URI:         "file:///Users/u/.lmstudio/models/mlx-community/Qwen3-Coder-30B-A3B-Instruct-4bit",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("Qwen3-Coder-30B-A3B-Instruct-4bit"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring(
				"model: /Users/u/.lmstudio/models/mlx-community/Qwen3-Coder-30B-A3B-Instruct-4bit"))
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("model: file://"))
		})
	})
})
