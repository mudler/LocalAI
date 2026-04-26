package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KittenTTSImporter", func() {
	Context("detection from HuggingFace", func() {
		It("matches KittenML/kitten-tts-nano-0.1 (owner + repo name)", func() {
			uri := "https://huggingface.co/KittenML/kitten-tts-nano-0.1"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: kitten-tts"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("tts"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("KittenML/kitten-tts-nano-0.1"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=kitten-tts for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "kitten-tts"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: kitten-tts"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.KittenTTSImporter{}
			Expect(imp.Name()).To(Equal("kitten-tts"))
			Expect(imp.Modality()).To(Equal("tts"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
