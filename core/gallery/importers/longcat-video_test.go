package importers_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LongCatVideoImporter", func() {
	var importer *importers.LongCatVideoImporter

	BeforeEach(func() {
		importer = &importers.LongCatVideoImporter{}
	})

	It("exposes video importer metadata", func() {
		Expect(importer.Name()).To(Equal("longcat-video"))
		Expect(importer.Modality()).To(Equal("video"))
		Expect(importer.AutoDetects()).To(BeTrue())
	})

	Describe("Match", func() {
		It("matches both official repositories", func() {
			for _, modelID := range []string{
				"meituan-longcat/LongCat-Video",
				"meituan-longcat/LongCat-Video-Avatar-1.5",
			} {
				details := importers.Details{
					URI: "https://huggingface.co/" + modelID,
					HuggingFace: &hfapi.ModelDetails{
						ModelID: modelID,
						Author:  "meituan-longcat",
					},
				}
				Expect(importer.Match(details)).To(BeTrue(), modelID)
			}
		})

		It("matches official hf URI forms without metadata", func() {
			Expect(importer.Match(importers.Details{
				URI: "https://huggingface.co/meituan-longcat/LongCat-Video-Avatar-1.5/tree/main/",
			})).To(BeTrue())
		})

		It("does not claim the same repository name under another owner", func() {
			Expect(importer.Match(importers.Details{
				URI: "https://huggingface.co/other-org/LongCat-Video",
			})).To(BeFalse())
		})

		It("honors an explicit backend preference", func() {
			Expect(importer.Match(importers.Details{
				URI:         "/models/LongCat-Video",
				Preferences: json.RawMessage(`{"backend":"longcat-video"}`),
			})).To(BeTrue())
			Expect(importer.Match(importers.Details{
				URI:         "hf://meituan-longcat/LongCat-Video",
				Preferences: json.RawMessage(`{"backend":"diffusers"}`),
			})).To(BeFalse())
		})
	})

	Describe("Import", func() {
		It("emits a base-model video configuration", func() {
			modelConfig, err := importer.Import(importers.Details{
				URI: "https://huggingface.co/meituan-longcat/LongCat-Video",
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("longcat-video"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: longcat-video"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: meituan-longcat/LongCat-Video"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("- video"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("attention_backend:sdpa"))
			Expect(modelConfig.ConfigFile).NotTo(ContainSubstring("use_distill:true"))
		})

		It("enables the distilled path for Avatar 1.5", func() {
			modelConfig, err := importer.Import(importers.Details{
				URI: "hf://meituan-longcat/LongCat-Video-Avatar-1.5",
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("longcat-video-avatar-1.5"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: meituan-longcat/LongCat-Video-Avatar-1.5"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("use_distill:true"))
		})
	})
})
