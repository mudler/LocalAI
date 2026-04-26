package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CoquiImporter", func() {
	Context("detection from HuggingFace", func() {
		It("matches coqui/XTTS-v2 (owner)", func() {
			uri := "https://huggingface.co/coqui/XTTS-v2"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: coqui"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("tts"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("coqui/XTTS-v2"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=coqui for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "coqui"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: coqui"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.CoquiImporter{}
			Expect(imp.Name()).To(Equal("coqui"))
			Expect(imp.Modality()).To(Equal("tts"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
