package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// hfWith builds Details carrying a synthetic HF file list so detection can be
// exercised without hitting the network.
func parakeetDetails(uri string, prefs string, files ...hfapi.ModelFile) importers.Details {
	return importers.Details{
		URI:         uri,
		Preferences: json.RawMessage(prefs),
		HuggingFace: &hfapi.ModelDetails{Files: files},
	}
}

var _ = Describe("ParakeetCppImporter", func() {
	imp := &importers.ParakeetCppImporter{}

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			Expect(imp.Name()).To(Equal("parakeet-cpp"))
			Expect(imp.Modality()).To(Equal("asr"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("detection (Match)", func() {
		It("matches an HF repo shipping a parakeet GGUF", func() {
			d := parakeetDetails("huggingface://mudler/parakeet-cpp-gguf", `{}`,
				hfapi.ModelFile{Path: "tdt_ctc-110m-f16.gguf"},
				hfapi.ModelFile{Path: "README.md"},
			)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("matches a direct URL to a parakeet GGUF", func() {
			d := parakeetDetails("https://huggingface.co/mudler/parakeet-cpp-gguf/resolve/main/rnnt-0.6b-q4_k.gguf", `{}`)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("honours preferences.backend=parakeet-cpp for arbitrary URIs", func() {
			d := parakeetDetails("https://example.com/whatever", `{"backend": "parakeet-cpp"}`)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("does NOT claim a generic llama-style GGUF", func() {
			d := parakeetDetails("huggingface://someorg/some-llm-gguf", `{}`,
				hfapi.ModelFile{Path: "llama-3-8b-instruct-q4_k_m.gguf"},
			)
			Expect(imp.Match(d)).To(BeFalse())
		})

		It("does NOT claim the upstream NeMo repo (.nemo, no GGUF)", func() {
			d := parakeetDetails("huggingface://nvidia/parakeet-tdt_ctc-110m", `{}`,
				hfapi.ModelFile{Path: "parakeet-tdt_ctc-110m.nemo"},
			)
			Expect(imp.Match(d)).To(BeFalse())
		})
	})

	Context("import (Import)", func() {
		It("picks the default quant (q4_k) from a multi-quant HF repo", func() {
			d := parakeetDetails("huggingface://mudler/parakeet-cpp-gguf", `{"name":"parakeet-110m"}`,
				hfapi.ModelFile{Path: "tdt_ctc-110m-f16.gguf", URL: "https://hf/f16", SHA256: "aaa"},
				hfapi.ModelFile{Path: "tdt_ctc-110m-q4_k.gguf", URL: "https://hf/q4k", SHA256: "bbb"},
				hfapi.ModelFile{Path: "tdt_ctc-110m-q8_0.gguf", URL: "https://hf/q8", SHA256: "ccc"},
			)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.ConfigFile).To(ContainSubstring("backend: parakeet-cpp"), fmt.Sprintf("%+v", cfg))
			Expect(cfg.ConfigFile).To(ContainSubstring("transcript"))
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/q4k"), "default quant should be q4_k")
			Expect(cfg.Files[0].Filename).To(ContainSubstring("parakeet-cpp/models/parakeet-110m/tdt_ctc-110m-q4_k.gguf"))
		})

		It("honours a preferred quantization override", func() {
			d := parakeetDetails("huggingface://mudler/parakeet-cpp-gguf", `{"name":"p","quantizations":"q8_0"}`,
				hfapi.ModelFile{Path: "tdt_ctc-110m-f16.gguf", URL: "https://hf/f16"},
				hfapi.ModelFile{Path: "tdt_ctc-110m-q8_0.gguf", URL: "https://hf/q8"},
			)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/q8"))
		})

		It("uses the exact file for a direct GGUF URL", func() {
			d := parakeetDetails("https://huggingface.co/mudler/parakeet-cpp-gguf/resolve/main/ctc-0.6b-q5_k.gguf", `{"name":"ctc"}`)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].Filename).To(ContainSubstring("parakeet-cpp/models/ctc/ctc-0.6b-q5_k.gguf"))
		})
	})
})
