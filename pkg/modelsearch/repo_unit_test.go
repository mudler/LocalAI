package modelsearch_test

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("quantLabel", func() {
	DescribeTable("reads the quantization from a file name",
		func(in, want string) { Expect(modelsearch.QuantLabel(in)).To(Equal(want)) },
		Entry("lower case", "qwen2.5-0.5b-instruct-q4_k_m.gguf", "Q4_K_M"),
		Entry("unsloth dynamic", "Qwen3-30B-A3B-UD-Q8_K_XL.gguf", "UD-Q8_K_XL"),
		Entry("shard suffix", "Qwen3-30B-A3B-BF16-00001-of-00002.gguf", "BF16"),
		Entry("iq", "Llama-3.2-1B-Instruct-IQ4_XS.gguf", "IQ4_XS"),
		Entry("float", "model-F16.gguf", "F16"),
		Entry("none", "mystery.gguf", "mystery"),
		Entry("qat", "gemma-3-27b-it-qat-q4_0.gguf", "Q4_0"),
	)
})

var _ = Describe("readmeSummary", func() {
	It("returns the first paragraph of prose", func() {
		card := "---\nlicense: apache-2.0\ntags:\n- chat\n---\n\n# Title\n\n[![badge](https://x/y.svg)](https://x)\n\n<p align=\"center\"><img src=\"x\"></p>\n\n## Introduction\n\nQwen2.5 is the **latest** series of [Qwen](https://q.example) large language models, with better coding.\n\nSecond paragraph.\n"
		Expect(modelsearch.ReadmeSummary(card)).To(Equal("Qwen2.5 is the latest series of Qwen large language models, with better coding."))
	})
	It("is empty for a card with no prose", func() {
		Expect(modelsearch.ReadmeSummary("---\nlicense: mit\n---\n# Only a title\n")).To(BeEmpty())
	})
	It("cuts a long paragraph at a word", func() {
		long := modelsearch.ReadmeSummary(strings.Repeat("word ", 200))
		Expect(len(long)).To(BeNumerically("<=", 404))
		Expect(long).To(HaveSuffix("..."))
	})
})

var _ = DescribeTable("modelsearch.ParseRepoURI",
	func(in string, want modelsearch.RepoRef, ok bool) {
		got, err := modelsearch.ParseRepoURI(in)
		Expect(err == nil).To(Equal(ok))
		Expect(got).To(Equal(want))
	},
	Entry("huggingface scheme", "huggingface://acme/Tiny-GGUF", modelsearch.RepoRef{Provider: modelsearch.HuggingFaceName, ID: "acme/Tiny-GGUF"}, true),
	Entry("hf scheme with a file", "hf://acme/Tiny-GGUF/model-q4_k_m.gguf", modelsearch.RepoRef{Provider: modelsearch.HuggingFaceName, ID: "acme/Tiny-GGUF"}, true),
	Entry("page url", "https://huggingface.co/acme/Tiny-GGUF", modelsearch.RepoRef{Provider: modelsearch.HuggingFaceName, ID: "acme/Tiny-GGUF"}, true),
	Entry("bare id", "acme/Tiny-GGUF", modelsearch.RepoRef{Provider: modelsearch.HuggingFaceName, ID: "acme/Tiny-GGUF"}, true),
	Entry("pirateface", "pirateface://acme/Tiny-GGUF", modelsearch.RepoRef{Provider: modelsearch.PirateFaceName, ID: "acme/Tiny-GGUF"}, true),
	Entry("magnet", "magnet:?xt=urn:btih:aa", modelsearch.RepoRef{}, false),
	Entry("oci", "oci://registry/x", modelsearch.RepoRef{}, false),
	Entry("empty", "", modelsearch.RepoRef{}, false),
	Entry("one word", "justaname", modelsearch.RepoRef{}, false),
)
