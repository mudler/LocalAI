package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// privacyFilterDetails builds Details carrying a synthetic HF file list so
// detection can be exercised without hitting the network.
func privacyFilterDetails(uri string, prefs string, files ...hfapi.ModelFile) importers.Details {
	return importers.Details{
		URI:         uri,
		Preferences: json.RawMessage(prefs),
		HuggingFace: &hfapi.ModelDetails{Files: files},
	}
}

var _ = Describe("PrivacyFilterImporter", func() {
	imp := &importers.PrivacyFilterImporter{}

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			Expect(imp.Name()).To(Equal("privacy-filter"))
			Expect(imp.Modality()).To(Equal("text"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("detection (Match)", func() {
		It("matches an HF repo shipping a privacy-filter GGUF", func() {
			d := privacyFilterDetails("huggingface://LocalAI-io/privacy-filter-multilingual-GGUF", "",
				hfapi.ModelFile{Path: "privacy-filter-multilingual-f16.gguf", URL: "https://hf/f16"})
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("matches a direct URL to a privacy-filter GGUF", func() {
			d := privacyFilterDetails("https://hf/resolve/main/privacy-filter-multilingual-f16.gguf", "")
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("matches the GGUF distribution repo by name when HF metadata is absent", func() {
			d := importers.Details{URI: "huggingface://LocalAI-io/privacy-filter-multilingual-GGUF", Preferences: json.RawMessage("")}
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("honours preferences.backend=privacy-filter for arbitrary URIs", func() {
			d := privacyFilterDetails("huggingface://some/unrelated-repo", `{"backend":"privacy-filter"}`)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("does NOT claim a generic llama-style GGUF", func() {
			d := privacyFilterDetails("huggingface://TheBloke/Llama-2-7B-GGUF", "",
				hfapi.ModelFile{Path: "llama-2-7b.Q4_K_M.gguf", URL: "https://hf/llama"})
			Expect(imp.Match(d)).To(BeFalse())
		})

		It("does NOT claim the upstream safetensors source repo (no GGUF)", func() {
			d := privacyFilterDetails("huggingface://OpenMed/privacy-filter-multilingual", "",
				hfapi.ModelFile{Path: "model.safetensors", URL: "https://hf/st"},
				hfapi.ModelFile{Path: "config.json", URL: "https://hf/cfg"})
			Expect(imp.Match(d)).To(BeFalse())
		})
	})

	Context("import (Import)", func() {
		It("emits a privacy-filter token_classify config from an HF GGUF repo", func() {
			d := privacyFilterDetails("huggingface://LocalAI-io/privacy-filter-multilingual-GGUF", `{"name":"pii"}`,
				hfapi.ModelFile{Path: "privacy-filter-multilingual-f16.gguf", URL: "https://hf/f16", SHA256: "abc"})
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.ConfigFile).To(ContainSubstring("backend: privacy-filter"), fmt.Sprintf("%+v", cfg))
			Expect(cfg.ConfigFile).To(ContainSubstring("token_classify"))
			Expect(cfg.ConfigFile).To(ContainSubstring("embeddings: true"))
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/f16"))
			Expect(cfg.Files[0].SHA256).To(Equal("abc"))
			Expect(cfg.Files[0].Filename).To(ContainSubstring("privacy-filter/models/pii/privacy-filter-multilingual-f16.gguf"))
		})

		It("prefers the highest-precision quant (f16) from a multi-quant repo", func() {
			d := privacyFilterDetails("huggingface://LocalAI-io/privacy-filter-multilingual-GGUF", "",
				hfapi.ModelFile{Path: "privacy-filter-multilingual-q4_k.gguf", URL: "https://hf/q4k"},
				hfapi.ModelFile{Path: "privacy-filter-multilingual-f16.gguf", URL: "https://hf/f16"})
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/f16"), "f16 should win over q4_k")
		})

		It("uses the exact file for a direct GGUF URL", func() {
			d := privacyFilterDetails("https://hf/resolve/main/privacy-filter-multilingual-f16.gguf", "")
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].Filename).To(ContainSubstring("privacy-filter/models/"))
			Expect(cfg.Files[0].Filename).To(ContainSubstring("privacy-filter-multilingual-f16.gguf"))
		})
	})
})
