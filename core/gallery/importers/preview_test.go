package importers_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PreviewImport", func() {
	files := func(paths ...string) []hfapi.ModelFile {
		out := make([]hfapi.ModelFile, 0, len(paths))
		for _, p := range paths {
			out = append(out, hfapi.ModelFile{Path: p, URL: "https://example.invalid/" + p})
		}
		return out
	}

	It("names the backend and the file the chosen quantization selects", func() {
		details := &hfapi.ModelDetails{
			ModelID: "acme/tiny-GGUF",
			Files:   files("tiny-q4_k_m.gguf", "tiny-q8_0.gguf", "README.md"),
		}
		p, err := importers.PreviewImport("huggingface://acme/tiny-GGUF", details, json.RawMessage(`{"quantizations":"q8_0"}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(p.Backend).To(Equal("llama-cpp"))
		Expect(p.Importer).To(Equal("llama-cpp"))
		Expect(p.Files).To(HaveLen(1))
		Expect(p.Files[0]).To(HaveSuffix("tiny-q8_0.gguf"))
	})

	It("reports an error when no importer matches", func() {
		_, err := importers.PreviewImport("huggingface://acme/none", &hfapi.ModelDetails{ModelID: "acme/none", Files: files("notes.txt")}, nil)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("QuantMatches", func() {
	It("matches whole tokens only", func() {
		Expect(importers.QuantMatches("model-BF16.gguf", "f16")).To(BeFalse())
		Expect(importers.QuantMatches("model-F16.gguf", "f16")).To(BeTrue())
		Expect(importers.QuantMatches("model-UD-Q4_K_XL.gguf", "q4_k_xl")).To(BeTrue())
	})
})
