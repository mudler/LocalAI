package importers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("huggingFaceRepoID", func() {
	DescribeTable("normalizes the ways a repository is written",
		func(in string) {
			Expect(huggingFaceRepoID(in)).To(Equal("owner/Some-Model-GGUF"))
		},
		Entry("https", "https://huggingface.co/owner/Some-Model-GGUF"),
		Entry("http", "http://huggingface.co/owner/Some-Model-GGUF"),
		Entry("www and trailing slash", "https://www.huggingface.co/owner/Some-Model-GGUF/"),
		Entry("tree suffix", "https://huggingface.co/owner/Some-Model-GGUF/tree/main"),
		Entry("query string", "https://huggingface.co/owner/Some-Model-GGUF?show_file_info=a.gguf"),
		Entry("no scheme", "huggingface.co/owner/Some-Model-GGUF"),
		Entry("no scheme, www", "www.huggingface.co/owner/Some-Model-GGUF"),
		Entry("hf.co", "hf.co/owner/Some-Model-GGUF"),
		Entry("huggingface scheme", "huggingface://owner/Some-Model-GGUF"),
		Entry("hf scheme", "hf://owner/Some-Model-GGUF"),
		Entry("bare ID", "owner/Some-Model-GGUF"),
		Entry("surrounding whitespace", "  huggingface.co/owner/Some-Model-GGUF\n"),
	)
})
