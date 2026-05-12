package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LiquidAudioImporter", func() {
	Context("detection from HuggingFace", func() {
		It("matches LiquidAI/LFM2.5-Audio-1.5B", func() {
			uri := "https://huggingface.co/LiquidAI/LFM2.5-Audio-1.5B"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: liquid-audio"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("LiquidAI/LFM2.5-Audio-1.5B"))
		})

		It("matches LiquidAI/LFM2-Audio-1.5B (older variant)", func() {
			uri := "https://huggingface.co/LiquidAI/LFM2-Audio-1.5B"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: liquid-audio"))
		})

		It("cedes -GGUF mirrors to the llama-cpp importer", func() {
			// LiquidAI/LFM2.5-Audio-1.5B-GGUF should NOT route to liquid-audio.
			// Once upstream PR #18641 lands and the GGUF gallery entry exists,
			// this is the path that lets users opt into the C++ runtime.
			uri := "https://huggingface.co/LiquidAI/LFM2.5-Audio-1.5B-GGUF"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("backend: liquid-audio"),
				fmt.Sprintf("GGUF repo should not match Python importer; got: %s", modelConfig.ConfigFile))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=liquid-audio for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "liquid-audio"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: liquid-audio"))
		})

		It("picks up the mode preference", func() {
			uri := "https://huggingface.co/LiquidAI/LFM2.5-Audio-1.5B"
			preferences := json.RawMessage(`{"mode": "asr"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("mode:asr"))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("transcript"))
		})

		It("picks up the voice preference", func() {
			uri := "https://huggingface.co/LiquidAI/LFM2.5-Audio-1.5B"
			preferences := json.RawMessage(`{"mode": "tts", "voice": "uk_male"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("voice:uk_male"))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.LiquidAudioImporter{}
			Expect(imp.Name()).To(Equal("liquid-audio"))
			Expect(imp.Modality()).To(Equal("tts"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
