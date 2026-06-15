package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KokoroImporter", func() {
	Context("detection from HuggingFace", func() {
		It("matches hexgrad/Kokoro-82M (repo name + .pth)", func() {
			uri := "https://huggingface.co/hexgrad/Kokoro-82M"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: kokoro"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("tts"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("hexgrad/Kokoro-82M"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=kokoro for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "kokoro"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: kokoro"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("kokoros preference disambiguation", func() {
		It("does not hijack preferences.backend=kokoros (pref-only)", func() {
			// The kokoros Rust runtime is pref-only (listed in
			// knownPrefOnlyBackends). The autodetect path for the kokoro
			// importer must NOT fire when the user explicitly selects the
			// kokoros backend for an arbitrary URI — if it did, DiscoverModelConfig
			// would incorrectly emit backend=kokoro.
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "kokoros"}`)

			_, err := importers.DiscoverModelConfig(uri, preferences)
			// kokoros has no importer, so discovery should not match anything.
			Expect(err).To(HaveOccurred(), "kokoros is pref-only — DiscoverModelConfig should not match any importer")
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.KokoroImporter{}
			Expect(imp.Name()).To(Equal("kokoro"))
			Expect(imp.Modality()).To(Equal("tts"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
