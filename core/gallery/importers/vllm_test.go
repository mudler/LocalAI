package importers_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("VLLMImporter", func() {
	var importer *VLLMImporter

	BeforeEach(func() {
		importer = &VLLMImporter{}
	})

	Context("Match", func() {
		It("should match when backend preference is vllm", func() {
			preferences := json.RawMessage(`{"backend": "vllm"}`)
			details := Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when HuggingFace details contain tokenizer.json", func() {
			hfDetails := &hfapi.ModelDetails{
				Files: []hfapi.ModelFile{
					{Path: "tokenizer.json"},
				},
			}
			details := Details{
				URI:         "https://huggingface.co/test/model",
				HuggingFace: hfDetails,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when HuggingFace details contain tokenizer_config.json", func() {
			hfDetails := &hfapi.ModelDetails{
				Files: []hfapi.ModelFile{
					{Path: "tokenizer_config.json"},
				},
			}
			details := Details{
				URI:         "https://huggingface.co/test/model",
				HuggingFace: hfDetails,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should not match when URI has no tokenizer files and no backend preference", func() {
			details := Details{
				URI: "https://example.com/model.bin",
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})

		It("should not match when backend preference is different", func() {
			preferences := json.RawMessage(`{"backend": "llama-cpp"}`)
			details := Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})

		It("should return false when JSON preferences are invalid", func() {
			preferences := json.RawMessage(`invalid json`)
			details := Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("should import model config with default name and description", func() {
			details := Details{
				URI: "https://huggingface.co/test/my-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-model"))
			Expect(modelConfig.Description).To(Equal("Imported from https://huggingface.co/test/my-model"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vllm"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: https://huggingface.co/test/my-model"))
		})

		It("should import model config with custom name and description from preferences", func() {
			preferences := json.RawMessage(`{"name": "custom-model", "description": "Custom description"}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("custom-model"))
			Expect(modelConfig.Description).To(Equal("Custom description"))
		})

		It("should use custom backend from preferences", func() {
			preferences := json.RawMessage(`{"backend": "vllm"}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: vllm"))
		})

		It("should handle invalid JSON preferences", func() {
			preferences := json.RawMessage(`invalid json`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			_, err := importer.Import(details)
			Expect(err).To(HaveOccurred())
		})

		It("should extract filename correctly from URI with path", func() {
			details := importers.Details{
				URI: "https://huggingface.co/test/path/to/model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("model"))
		})

		It("should include use_tokenizer_template in config", func() {
			details := Details{
				URI: "https://huggingface.co/test/my-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("use_tokenizer_template: true"))
		})

		It("should include known_usecases in config", func() {
			details := Details{
				URI: "https://huggingface.co/test/my-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("known_usecases:"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- chat"))
		})
	})
})
