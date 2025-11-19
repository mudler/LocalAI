package importers_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DiffuserImporter", func() {
	var importer *DiffuserImporter

	BeforeEach(func() {
		importer = &DiffuserImporter{}
	})

	Context("Match", func() {
		It("should match when backend preference is diffusers", func() {
			preferences := json.RawMessage(`{"backend": "diffusers"}`)
			details := Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when HuggingFace details contain model_index.json", func() {
			hfDetails := &hfapi.ModelDetails{
				Files: []hfapi.ModelFile{
					{Path: "model_index.json"},
				},
			}
			details := Details{
				URI:         "https://huggingface.co/test/model",
				HuggingFace: hfDetails,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should match when HuggingFace details contain scheduler config", func() {
			hfDetails := &hfapi.ModelDetails{
				Files: []hfapi.ModelFile{
					{Path: "scheduler/scheduler_config.json"},
				},
			}
			details := Details{
				URI:         "https://huggingface.co/test/model",
				HuggingFace: hfDetails,
			}

			result := importer.Match(details)
			Expect(result).To(BeTrue())
		})

		It("should not match when URI has no diffuser files and no backend preference", func() {
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
				URI: "https://huggingface.co/test/my-diffuser-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-diffuser-model"))
			Expect(modelConfig.Description).To(Equal("Imported from https://huggingface.co/test/my-diffuser-model"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: diffusers"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: https://huggingface.co/test/my-diffuser-model"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("pipeline_type: StableDiffusionPipeline"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("enable_parameters: negative_prompt,num_inference_steps"))
		})

		It("should import model config with custom name and description from preferences", func() {
			preferences := json.RawMessage(`{"name": "custom-diffuser", "description": "Custom diffuser model"}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("custom-diffuser"))
			Expect(modelConfig.Description).To(Equal("Custom diffuser model"))
		})

		It("should use custom pipeline_type from preferences", func() {
			preferences := json.RawMessage(`{"pipeline_type": "StableDiffusion3Pipeline"}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("pipeline_type: StableDiffusion3Pipeline"))
		})

		It("should use default pipeline_type when not specified", func() {
			details := Details{
				URI: "https://huggingface.co/test/my-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("pipeline_type: StableDiffusionPipeline"))
		})

		It("should use custom scheduler_type from preferences", func() {
			preferences := json.RawMessage(`{"scheduler_type": "k_dpmpp_2m"}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("scheduler_type: k_dpmpp_2m"))
		})

		It("should use cuda setting from preferences", func() {
			preferences := json.RawMessage(`{"cuda": true}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("cuda: true"))
		})

		It("should use custom enable_parameters from preferences", func() {
			preferences := json.RawMessage(`{"enable_parameters": "num_inference_steps,guidance_scale"}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("enable_parameters: num_inference_steps,guidance_scale"))
		})

		It("should use custom backend from preferences", func() {
			preferences := json.RawMessage(`{"backend": "diffusers"}`)
			details := Details{
				URI:         "https://huggingface.co/test/my-model",
				Preferences: preferences,
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: diffusers"))
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

		It("should include known_usecases as image in config", func() {
			details := Details{
				URI: "https://huggingface.co/test/my-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("known_usecases:"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- image"))
		})

		It("should include diffusers configuration in config", func() {
			details := Details{
				URI: "https://huggingface.co/test/my-model",
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("diffusers:"))
		})
	})
})
