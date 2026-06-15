package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("QwenASRImporter", func() {
	Context("detection from HuggingFace", func() {
		It("matches Qwen/Qwen3-ASR-1.7B (owner + ASR in name)", func() {
			uri := "https://huggingface.co/Qwen/Qwen3-ASR-1.7B"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: qwen-asr"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("Qwen/Qwen3-ASR-1.7B"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=qwen-asr for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "qwen-asr"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: qwen-asr"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.QwenASRImporter{}
			Expect(imp.Name()).To(Equal("qwen-asr"))
			Expect(imp.Modality()).To(Equal("asr"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
