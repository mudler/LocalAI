package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ACEStepImporter", func() {
	Context("detection from HuggingFace", func() {
		// ACE-Step/ACE-Step-v1-3.5B is the reference public checkpoint for
		// the ACE-Step music generation model. Detection must match on the
		// repo name substring so third-party forks and quantised mirrors
		// (e.g. Serveurperso/ACE-Step-1.5-GGUF) route to the same backend.
		It("matches ACE-Step/ACE-Step-v1-3.5B (repo name contains ACE-Step)", func() {
			uri := "https://huggingface.co/ACE-Step/ACE-Step-v1-3.5B"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: ace-step"), fmt.Sprintf("Model config: %+v", modelConfig))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("ACE-Step/ACE-Step-v1-3.5B"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("preference override", func() {
		It("honours preferences.backend=ace-step for arbitrary URIs", func() {
			uri := "https://example.com/some-unrelated-model"
			preferences := json.RawMessage(`{"backend": "ace-step"}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)

			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error: %v", err))
			Expect(modelConfig.ConfigFile).To(ContainSubstring("backend: ace-step"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			imp := &importers.ACEStepImporter{}
			Expect(imp.Name()).To(Equal("ace-step"))
			Expect(imp.Modality()).To(Equal("image"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})
})
