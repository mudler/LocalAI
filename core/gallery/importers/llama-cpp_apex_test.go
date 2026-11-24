package importers_test

import (
	. "github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The listing of a public APEX repository: eight quant variants, an
// importance matrix, a projector and a DSpark drafter. No pipeline tag.
var _ = Describe("APEX GGUF repository import", func() {
	It("imports a repository with APEX quants, imatrix, mmproj and drafter", func() {
		var listing []hfapi.ModelFile
		for _, p := range []string{
			"Inkling-2-Mini-APEX-Balanced.gguf",
			"Inkling-2-Mini-APEX-Compact.gguf",
			"Inkling-2-Mini-APEX-I-Balanced.gguf",
			"Inkling-2-Mini-APEX-I-Compact.gguf",
			"Inkling-2-Mini-APEX-I-Mini.gguf",
			"Inkling-2-Mini-APEX-I-Nano.gguf",
			"Inkling-2-Mini-APEX-I-Quality.gguf",
			"Inkling-2-Mini-APEX-Quality.gguf",
			"README.md",
			"dspark-Inkling-2-Mini-Q8_0.gguf",
			"imatrix.gguf",
			"mmproj-Inkling-2-Mini-F16.gguf",
		} {
			listing = append(listing, hfapi.ModelFile{Path: p, SHA256: "sha-" + p,
				URL: "https://huggingface.co/acme/Inkling-2-Mini-APEX-GGUF/resolve/main/" + p})
		}
		details := &hfapi.ModelDetails{ModelID: "acme/Inkling-2-Mini-APEX-GGUF", Files: listing}
		p, err := PreviewImport("huggingface.co/acme/Inkling-2-Mini-APEX-GGUF", details, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.Importer).To(Equal("llama-cpp"))
		Expect(p.Files).To(ContainElement(HaveSuffix("/mmproj-Inkling-2-Mini-F16.gguf")))
		Expect(p.Files).To(ContainElement(HaveSuffix("/dspark-Inkling-2-Mini-Q8_0.gguf")))
		Expect(p.Files).To(HaveLen(3))
		Expect(p.Files[0]).To(HaveSuffix("-APEX-Quality.gguf"))
		Expect(p.Files).ToNot(ContainElement(HaveSuffix("imatrix.gguf")))
	})
})
