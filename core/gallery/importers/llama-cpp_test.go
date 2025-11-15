package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LlamaCPPImporter", func() {
	var importer *LlamaCPPImporter

	BeforeEach(func() {
		importer = &LlamaCPPImporter{}
	})

	Context("Match", func() {
		It("should match when URI ends with .gguf", func() {
			details := Details{
				URI: "https://example.com/model.gguf",
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when backend preference is llama-cpp", func() {
			preferences := json.RawMessage(`{"backend": "llama-cpp"}`)
			details := Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should not match when URI does not end with .gguf and no backend preference", func() {
			details := Details{
				URI: "https://example.com/model.bin",
			}

			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})

		It("should not match when backend preference is different", func() {
			preferences := json.RawMessage(`{"backend": "mlx"}`)
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
				URI:         "https://example.com/model.gguf",
				Preferences: preferences,
			}

			// Invalid JSON causes Match to return false early
			result := importer.Match(details)
			Expect(result).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("should import model config with default name and description", func() {
			details := Details{
				URI: "https://example.com/my-model.gguf",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-model.gguf"))
			Expect(modelConfig.Description).To(Equal("Imported from https://example.com/my-model.gguf"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: llama-cpp"))
			Expect(len(modelConfig.Files)).To(Equal(1), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://example.com/my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("should import model config with custom name and description from preferences", func() {
			preferences := json.RawMessage(`{"name": "custom-model", "description": "Custom description"}`)
			details := Details{
				URI:         "https://example.com/my-model.gguf",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("custom-model"))
			Expect(modelConfig.Description).To(Equal("Custom description"))
			Expect(len(modelConfig.Files)).To(Equal(1), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://example.com/my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("my-model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("should handle invalid JSON preferences", func() {
			preferences := json.RawMessage(`invalid json`)
			details := Details{
				URI:         "https://example.com/my-model.gguf",
				Preferences: preferences,
			}

			_, err := importer.Import(details)
			Expect(err).To(HaveOccurred())
		})

		It("should extract filename correctly from URI with path", func() {
			details := importers.Details{
				URI: "https://example.com/path/to/model.gguf",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(len(modelConfig.Files)).To(Equal(1), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].URI).To(Equal("https://example.com/path/to/model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.Files[0].Filename).To(Equal("model.gguf"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})
})
