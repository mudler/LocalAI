package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SileroVADImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.SileroVADImporter{}
			Expect(imp.Name()).To(Equal("silero-vad"))
			Expect(imp.Modality()).To(Equal("vad"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("Match", func() {
		It("matches when backend preference is silero-vad", func() {
			imp := &importers.SileroVADImporter{}
			preferences := json.RawMessage(`{"backend": "silero-vad"}`)
			details := importers.Details{
				URI:         "https://example.com/some-model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when HF repo ships silero_vad.onnx", func() {
			imp := &importers.SileroVADImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/snakers4/silero-vad",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "snakers4/silero-vad",
					Author:  "snakers4",
					Files: []hfapi.ModelFile{
						{Path: "silero_vad.onnx"},
					},
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches snakers4 owner with ONNX files even without the canonical filename", func() {
			imp := &importers.SileroVADImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/snakers4/silero-vad",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "snakers4/silero-vad",
					Author:  "snakers4",
					Files: []hfapi.ModelFile{
						{Path: "some-other.onnx"},
					},
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches via URI fallback when HuggingFace details are missing", func() {
			imp := &importers.SileroVADImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/snakers4/silero-vad",
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("does not match unrelated repos without silero_vad.onnx or snakers4 owner", func() {
			imp := &importers.SileroVADImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/someone/random-model",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "someone/random-model",
					Author:  "someone",
					Files: []hfapi.ModelFile{
						{Path: "config.json"},
					},
				},
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("returns false for invalid preferences JSON", func() {
			imp := &importers.SileroVADImporter{}
			preferences := json.RawMessage(`not valid json`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("produces a YAML with backend silero-vad and the vad known_usecase", func() {
			imp := &importers.SileroVADImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/snakers4/silero-vad",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "snakers4/silero-vad",
					Author:  "snakers4",
					Files: []hfapi.ModelFile{
						{Path: "silero_vad.onnx", URL: "https://huggingface.co/snakers4/silero-vad/resolve/main/silero_vad.onnx"},
					},
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: silero-vad"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- vad"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("respects custom name and description from preferences", func() {
			imp := &importers.SileroVADImporter{}
			preferences := json.RawMessage(`{"name": "my-vad", "description": "Custom"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/snakers4/silero-vad",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "snakers4/silero-vad",
					Author:  "snakers4",
					Files: []hfapi.ModelFile{
						{Path: "silero_vad.onnx"},
					},
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-vad"))
			Expect(modelConfig.Description).To(Equal("Custom"))
		})
	})

	Context("detection from HuggingFace", func() {
		It("matches snakers4/silero-vad via live HF metadata", func() {
			uri := "https://huggingface.co/snakers4/silero-vad"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: silero-vad"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})
})
