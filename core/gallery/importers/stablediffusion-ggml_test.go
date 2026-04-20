package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StableDiffusionGGMLImporter", func() {
	Context("detection from HuggingFace", func() {
		// city96/FLUX.1-dev-gguf is the canonical community GGUF mirror for
		// FLUX.1-dev and ships a flat tree of .gguf quantisations
		// (flux1-dev-Q4_K.gguf, flux1-dev-Q8_0.gguf, etc.). Detection must
		// route this to stablediffusion-ggml (and NOT to llama-cpp, which
		// otherwise steals every .gguf repo).
		It("matches a HF repo with GGUF files whose owner/repo contains flux/sd/sdxl tokens", func() {
			uri := "https://huggingface.co/city96/FLUX.1-dev-gguf"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: stablediffusion-ggml"), fmt.Sprintf("Model config: %+v", modelConfig))
		})

		It("matches a raw .gguf URL containing flux/sd arch tokens", func() {
			uri := "https://example.com/models/flux1-dev-Q4_K.gguf"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: stablediffusion-ggml"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=stablediffusion-ggml for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "stablediffusion-ggml"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: stablediffusion-ggml"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.StableDiffusionGGMLImporter{}
			Expect(imp.Name()).To(Equal("stablediffusion-ggml"))
			Expect(imp.Modality()).To(Equal("image"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
