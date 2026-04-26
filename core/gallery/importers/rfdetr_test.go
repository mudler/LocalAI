package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RFDetrImporter", func() {
	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.RFDetrImporter{}
			Expect(imp.Name()).To(Equal("rfdetr"))
			Expect(imp.Modality()).To(Equal("detection"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("Match", func() {
		It("matches when backend preference is rfdetr", func() {
			imp := &importers.RFDetrImporter{}
			preferences := json.RawMessage(`{"backend": "rfdetr"}`)
			details := importers.Details{
				URI:         "https://example.com/some-model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when the repo name contains 'rf-detr' (case-insensitive)", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/roboflow/rf-detr-base",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "roboflow/rf-detr-base",
					Author:  "roboflow",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches when the repo name contains 'rfdetr' (case-insensitive)", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/some/rfdetr-whatever",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "some/rfdetr-whatever",
					Author:  "some",
				},
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("matches via URI fallback when HuggingFace details are missing", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/roboflow/rf-detr-base",
			}

			Expect(imp.Match(details)).To(BeTrue())
		})

		It("does not match unrelated repos without rfdetr signals", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/meta-llama/Llama-3-8B",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "meta-llama/Llama-3-8B",
					Author:  "meta-llama",
				},
			}

			Expect(imp.Match(details)).To(BeFalse())
		})

		It("returns false for invalid preferences JSON", func() {
			imp := &importers.RFDetrImporter{}
			preferences := json.RawMessage(`not valid json`)
			details := importers.Details{
				URI:         "https://example.com/model",
				Preferences: preferences,
			}

			Expect(imp.Match(details)).To(BeFalse())
		})
	})

	Context("Import", func() {
		It("produces a YAML with backend rfdetr and the repo as the model", func() {
			imp := &importers.RFDetrImporter{}
			details := importers.Details{
				URI: "https://huggingface.co/roboflow/rf-detr-base",
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "roboflow/rf-detr-base",
					Author:  "roboflow",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: rfdetr"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("roboflow/rf-detr-base"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("respects custom name and description from preferences", func() {
			imp := &importers.RFDetrImporter{}
			preferences := json.RawMessage(`{"name": "my-detr", "description": "Custom"}`)
			details := importers.Details{
				URI:         "https://huggingface.co/roboflow/rf-detr-base",
				Preferences: preferences,
				HuggingFace: &hfapi.ModelDetails{
					ModelID: "roboflow/rf-detr-base",
					Author:  "roboflow",
				},
			}

			modelConfig, err := imp.Import(details)

			Expect(err).ToNot(HaveOccurred())
			Expect(modelConfig.Name).To(Equal("my-detr"))
			Expect(modelConfig.Description).To(Equal("Custom"))
		})
	})
})
