package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mossDetails builds Details carrying a synthetic HF file list so detection can
// be exercised without hitting the network.
func mossDetails(uri string, prefs string, files ...hfapi.ModelFile) importers.Details {
	return importers.Details{
		URI:         uri,
		Preferences: json.RawMessage(prefs),
		HuggingFace: &hfapi.ModelDetails{Files: files},
	}
}

var _ = Describe("MossTranscribeCppImporter", func() {
	imp := &importers.MossTranscribeCppImporter{}

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			Expect(imp.Name()).To(Equal("moss-transcribe-cpp"))
			Expect(imp.Modality()).To(Equal("asr"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("detection (Match)", func() {
		It("matches an HF repo shipping a moss-transcribe GGUF", func() {
			d := mossDetails("huggingface://mudler/moss-transcribe.cpp-gguf", `{}`,
				hfapi.ModelFile{Path: "moss-transcribe-q5_k.gguf"},
				hfapi.ModelFile{Path: "README.md"},
			)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("matches a direct URL to a moss-transcribe GGUF", func() {
			d := mossDetails("https://huggingface.co/mudler/moss-transcribe.cpp-gguf/resolve/main/moss-transcribe-q8_0.gguf", `{}`)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("honours preferences.backend=moss-transcribe-cpp for arbitrary URIs", func() {
			d := mossDetails("https://example.com/whatever", `{"backend": "moss-transcribe-cpp"}`)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("does NOT claim a generic llama-style GGUF", func() {
			d := mossDetails("huggingface://someorg/some-llm-gguf", `{}`,
				hfapi.ModelFile{Path: "llama-3-8b-instruct-q4_k_m.gguf"},
			)
			Expect(imp.Match(d)).To(BeFalse())
		})
	})

	Context("import (Import)", func() {
		It("picks the default quant (q5_k) from a multi-quant HF repo", func() {
			d := mossDetails("huggingface://mudler/moss-transcribe.cpp-gguf", `{"name":"moss-transcribe"}`,
				hfapi.ModelFile{Path: "moss-transcribe-f16.gguf", URL: "https://hf/f16", SHA256: "aaa"},
				hfapi.ModelFile{Path: "moss-transcribe-q5_k.gguf", URL: "https://hf/q5k", SHA256: "bbb"},
				hfapi.ModelFile{Path: "moss-transcribe-q8_0.gguf", URL: "https://hf/q8", SHA256: "ccc"},
			)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.ConfigFile).To(ContainSubstring("backend: moss-transcribe-cpp"), fmt.Sprintf("%+v", cfg))
			Expect(cfg.ConfigFile).To(ContainSubstring("transcript"))
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/q5k"), "default quant should be q5_k")
			Expect(cfg.Files[0].Filename).To(ContainSubstring("moss-transcribe-cpp/models/moss-transcribe/moss-transcribe-q5_k.gguf"))
		})

		It("honours a preferred quantization override", func() {
			d := mossDetails("huggingface://mudler/moss-transcribe.cpp-gguf", `{"name":"m","quantizations":"q8_0"}`,
				hfapi.ModelFile{Path: "moss-transcribe-q5_k.gguf", URL: "https://hf/q5k"},
				hfapi.ModelFile{Path: "moss-transcribe-q8_0.gguf", URL: "https://hf/q8"},
			)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/q8"))
		})

		It("uses the exact file for a direct GGUF URL", func() {
			d := mossDetails("https://huggingface.co/mudler/moss-transcribe.cpp-gguf/resolve/main/moss-transcribe-q5_k.gguf", `{"name":"moss"}`)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].Filename).To(ContainSubstring("moss-transcribe-cpp/models/moss/moss-transcribe-q5_k.gguf"))
		})
	})
})
