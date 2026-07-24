package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Trellis2CppImporter", func() {
	Context("detection from HuggingFace", func() {
		// LocalAI-io/TRELLIS.2-4B-GGUF is the canonical GGUF conversion of
		// microsoft/TRELLIS.2-4B produced by the trellis2cpp converters.
		// Detection must route it to trellis2cpp (and NOT to llama-cpp,
		// which otherwise steals every .gguf repo).
		It("matches the TRELLIS.2 GGUF repo and imports the full component set", func() {
			uri := "https://huggingface.co/LocalAI-io/TRELLIS.2-4B-GGUF"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: trellis2cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("known_usecases"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("FLAG_3D"))
			// The pipeline spans three repos; the import must carry the whole
			// set, anchored on ss_flow.
			Expect(modelConfig.Files).To(HaveLen(10))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("model: ss_flow_f16.gguf"))
		})

		It("matches a raw .gguf URL named after a distinctive pipeline component", func() {
			uri := "https://example.com/models/tex_slat_flow_512_f16.gguf"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: trellis2cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=trellis2cpp for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "trellis2cpp"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: trellis2cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("does not override a different explicit backend", func() {
			imp := &importers.Trellis2CppImporter{}
			match := imp.Match(importers.Details{
				URI:         "https://example.com/models/tex_slat_flow_512_f16.gguf",
				Preferences: json.RawMessage(`{"backend": "llama-cpp"}`),
			})

			Expect(match).To(BeFalse())
		})

		It("still auto-detects when the backend preference is empty", func() {
			imp := &importers.Trellis2CppImporter{}
			match := imp.Match(importers.Details{
				URI:         "https://example.com/models/tex_slat_flow_512_f16.gguf",
				Preferences: json.RawMessage(`{"backend": ""}`),
			})

			Expect(match).To(BeTrue())
		})
	})

	Context("negative detection", func() {
		It("does not claim an unrelated raw .gguf URL", func() {
			imp := &importers.Trellis2CppImporter{}
			match := imp.Match(importers.Details{
				URI:         "https://example.com/models/llama-3-8b-Q4_K.gguf",
				Preferences: json.RawMessage(`{}`),
			})
			Expect(match).To(BeFalse())
		})

		It("does not claim a bare dino_f16.gguf (too generic a name)", func() {
			imp := &importers.Trellis2CppImporter{}
			match := imp.Match(importers.Details{
				URI:         "https://example.com/models/dino_f16.gguf",
				Preferences: json.RawMessage(`{}`),
			})
			Expect(match).To(BeFalse())
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.Trellis2CppImporter{}
			Expect(imp.Name()).To(Equal("trellis2cpp"))
			Expect(imp.Modality()).To(Equal("3d"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
