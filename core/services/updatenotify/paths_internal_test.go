package updatenotify

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("withoutPaths", func() {
	DescribeTable("shortens absolute paths and leaves the rest alone",
		func(in, want string) {
			Expect(withoutPaths(in)).To(Equal(want))
		},
		Entry("absolute path", "open /srv/models/tiny.gguf: no such file", "open tiny.gguf: no such file"),
		Entry("path at the start", "/srv/models/tiny.gguf is missing", "tiny.gguf is missing"),
		Entry("after an equals sign", "path=/srv/models/tiny.gguf failed", "path=tiny.gguf failed"),
		Entry("quoted", `cannot read "/srv/models/tiny.gguf"`, `cannot read "tiny.gguf"`),
		Entry("in parentheses", "failed (/srv/models/tiny.gguf)", "failed (tiny.gguf)"),
		Entry("windows path", `open C:\models\tiny.gguf: denied`, "open tiny.gguf: denied"),
		Entry("url", "get https://example.com/org/repo/file.gguf: 404", "get https://example.com/org/repo/file.gguf: 404"),
		Entry("scheme-less url", "huggingface.co/org/repo/file.gguf", "huggingface.co/org/repo/file.gguf"),
		Entry("relative path", "models/sub/x.gguf", "models/sub/x.gguf"),
		Entry("digits", "1/2/3", "1/2/3"),
		Entry("relative path then absolute", "models/a.gguf and /srv/x/b.gguf", "models/a.gguf and b.gguf"),
		Entry("no path", "disk full", "disk full"),
	)
})
