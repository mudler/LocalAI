package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BarkImporter", func() {
	Context("detection from HuggingFace", func() {
		It("matches suno/bark (owner + repo name prefix)", func() {
			uri := "https://huggingface.co/suno/bark"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: bark"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("tts"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("suno/bark"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=bark for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "bark"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: bark"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.BarkImporter{}
			Expect(imp.Name()).To(Equal("bark"))
			Expect(imp.Modality()).To(Equal("tts"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
