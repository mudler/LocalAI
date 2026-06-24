package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OmniVoice pref-only guard", func() {
	Context("With only a bare OmniVoice GGUF URI", func() {
		It("does not auto-import as omnivoice-cpp", func() {
			// omnivoice-cpp is a preference-only backend (listed in the
			// /backends/known registry with AutoDetect:false). No importer
			// emits it, so discovering a bare OmniVoice GGUF must never
			// silently resolve to omnivoice-cpp. It may legitimately match a
			// generic GGUF importer (e.g. llama-cpp) or error/be ambiguous —
			// the only hard requirement is that it is NOT omnivoice-cpp.
			uri := "huggingface://Serveurperso/OmniVoice-GGUF/omnivoice-base-Q8_0.gguf"
			preferences := json.RawMessage(`{}`)

			modelConfig, err := importers.DiscoverModelConfig(uri, preferences)
			if err != nil {
				// An error (including ambiguous) is acceptable for a pref-only backend.
				return
			}
			Expect(modelConfig.ConfigFile).ToNot(ContainSubstring("backend: omnivoice-cpp"), fmt.Sprintf("Model config: %+v", modelConfig))
		})
	})
})
