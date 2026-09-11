package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func registeredImporter(name string) importers.Importer {
	for _, importer := range importers.Registry() {
		if importer.Name() == name {
			return importer
		}
	}
	return nil
}

var _ = Describe("FunASRImporter", func() {
	Context("registry metadata", func() {
		It("registers an auto-detected ASR backend", func() {
			importer := registeredImporter("funasr")

			Expect(importer).ToNot(BeNil())
			if importer == nil {
				return
			}
			Expect(importer.Modality()).To(Equal("asr"))
			Expect(importer.AutoDetects()).To(BeTrue())
		})
	})

	Context("matching", func() {
		DescribeTable("matches only the official SenseVoiceSmall repository or an explicit preference",
			func(details importers.Details, expected bool) {
				importer := registeredImporter("funasr")
				Expect(importer).ToNot(BeNil())
				if importer == nil {
					return
				}
				Expect(importer.Match(details)).To(Equal(expected))
			},
			Entry("official FunAudioLLM/SenseVoiceSmall",
				importers.Details{
					URI: "https://huggingface.co/FunAudioLLM/SenseVoiceSmall",
					HuggingFace: &hfapi.ModelDetails{
						Author:  "FunAudioLLM",
						ModelID: "FunAudioLLM/SenseVoiceSmall",
					},
				},
				true,
			),
			Entry("same owner but a different model",
				importers.Details{
					URI: "https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512",
					HuggingFace: &hfapi.ModelDetails{
						Author:  "FunAudioLLM",
						ModelID: "FunAudioLLM/Fun-ASR-Nano-2512",
					},
				},
				false,
			),
			Entry("same model name under another owner",
				importers.Details{
					URI: "https://huggingface.co/community/SenseVoiceSmall",
					HuggingFace: &hfapi.ModelDetails{
						Author:  "community",
						ModelID: "community/SenseVoiceSmall",
					},
				},
				false,
			),
			Entry("explicit funasr preference for an arbitrary model",
				importers.Details{
					URI:         "iic/paraformer-zh",
					Preferences: json.RawMessage(`{"backend":"funasr"}`),
				},
				true,
			),
			Entry("another explicit backend prevents auto-detection",
				importers.Details{
					URI:         "https://huggingface.co/FunAudioLLM/SenseVoiceSmall",
					Preferences: json.RawMessage(`{"backend":"qwen-asr"}`),
					HuggingFace: &hfapi.ModelDetails{
						Author:  "FunAudioLLM",
						ModelID: "FunAudioLLM/SenseVoiceSmall",
					},
				},
				false,
			),
		)
	})

	Context("import", func() {
		It("maps the official Hugging Face model to the working ModelScope identifier", func() {
			importer := registeredImporter("funasr")
			Expect(importer).ToNot(BeNil())
			if importer == nil {
				return
			}
			details := importers.Details{
				URI: "https://huggingface.co/FunAudioLLM/SenseVoiceSmall",
				HuggingFace: &hfapi.ModelDetails{
					Author:  "FunAudioLLM",
					ModelID: "FunAudioLLM/SenseVoiceSmall",
				},
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: funasr"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: iic/SenseVoiceSmall"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- transcript"))
		})

		It("preserves an explicitly selected model identifier", func() {
			importer := registeredImporter("funasr")
			Expect(importer).ToNot(BeNil())
			if importer == nil {
				return
			}
			details := importers.Details{
				URI:         "iic/paraformer-zh",
				Preferences: json.RawMessage(`{"backend":"funasr"}`),
			}

			modelConfig, err := importer.Import(details)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: funasr"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: iic/paraformer-zh"))
		})
	})
})
