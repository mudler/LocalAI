package sanitize_test

import (
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/sanitize"
)

var _ = Describe("WithoutPaths", func() {
	DescribeTable("shortens absolute paths and leaves the rest alone",
		func(in, want string) {
			Expect(sanitize.WithoutPaths(in)).To(Equal(want))
		},
		Entry("absolute path", "open /srv/models/tiny.gguf: no such file", "open tiny.gguf: no such file"),
		Entry("path at the start", "/srv/models/tiny.gguf is missing", "tiny.gguf is missing"),
		Entry("after an equals sign", "path=/srv/models/tiny.gguf failed", "path=tiny.gguf failed"),
		Entry("quoted", `cannot read "/srv/models/tiny.gguf"`, `cannot read "tiny.gguf"`),
		Entry("in parentheses", "failed (/srv/models/tiny.gguf)", "failed (tiny.gguf)"),
		Entry("windows path", `open C:\models\tiny.gguf: denied`, "open tiny.gguf: denied"),
		Entry("home directory", "open ~/.cache/models/tiny.gguf: denied", "open tiny.gguf: denied"),
		Entry("home directory of a user", "open ~alice/models/tiny.gguf: denied", "open tiny.gguf: denied"),
		Entry("after a new line", "failed:\n/home/alice/x/b.gguf", "failed:\nb.gguf"),
		Entry("url", "get https://example.com/org/repo/file.gguf: 404", "get https://example.com/org/repo/file.gguf: 404"),
		Entry("scheme-less url", "huggingface.co/org/repo/file.gguf", "huggingface.co/org/repo/file.gguf"),
		Entry("relative path", "models/sub/x.gguf", "models/sub/x.gguf"),
		Entry("digits", "1/2/3", "1/2/3"),
		Entry("relative path then absolute", "models/a.gguf and /srv/x/b.gguf", "models/a.gguf and b.gguf"),
		Entry("no path", "disk full", "disk full"),
	)
})

var _ = Describe("Redact", func() {
	DescribeTable("removes what looks like a credential",
		func(in string, gone []string, kept string) {
			out := sanitize.Redact(in)
			for _, g := range gone {
				Expect(out).NotTo(ContainSubstring(g))
			}
			Expect(out).To(ContainSubstring(kept))
		},
		Entry("url userinfo", "get https://user:pass@registry.example.com/v2/x: 401",
			[]string{"user:pass", "user", "pass@"}, "https://registry.example.com/v2/x"),
		Entry("token in a query", "get https://host/x?token=s3cr3tvalue: 403", []string{"s3cr3tvalue"}, "token=[removed]"),
		Entry("bearer", "header Authorization: Bearer abcdef0123456789", []string{"abcdef0123456789"}, "[removed]"),
		Entry("short bearer", "Bearer abc refused", []string{"abc"}, "Bearer [removed] refused"),
		Entry("bearer alone", "sent Bearer abcdef0123456789 to the server", []string{"abcdef0123456789"}, "Bearer [removed]"),
		Entry("password", `password="hunter2!" refused`, []string{"hunter2"}, "password=[removed]"),
		Entry("api key", "api_key: abc123xyz refused", []string{"abc123xyz"}, "api_key=[removed]"),
		// Environment variables: "_" is a word character, so a key behind a
		// prefix has no word boundary.
		Entry("hugging face token variable", "env HF_TOKEN=hf_fakeFAKEfake000 set", []string{"hf_fakeFAKEfake000"}, "HF_TOKEN=[removed]"),
		Entry("api key variable", "OPENAI_API_KEY=fake-key-value-0000 exported", []string{"fake-key-value-0000"}, "OPENAI_API_KEY=[removed]"),
		Entry("secret access key variable", "AWS_SECRET_ACCESS_KEY=fakeSecretValue0000 read", []string{"fakeSecretValue0000"}, "AWS_SECRET_ACCESS_KEY=[removed]"),
		Entry("lower case variable", "db_password: fakepass0 refused", []string{"fakepass0"}, "db_password=[removed]"),
		Entry("json field", `body {"user":"x","password":"fake-pass-0"} refused`, []string{"fake-pass-0"}, "password"),
		Entry("json field with spaces", `{"api_key" : "fake-key-0"}`, []string{"fake-key-0"}, "api_key"),
		Entry("bare github token", "push with ghp_FAKEfakeFAKEfakeFAKEfake0000 failed", []string{"ghp_FAKEfakeFAKEfakeFAKEfake0000"}, "[removed]"),
		Entry("bare fine-grained github token", "github_pat_FAKE0fake0FAKE0fake0FAKE0 used", []string{"github_pat_FAKE0fake0FAKE0fake0FAKE0"}, "[removed]"),
		Entry("bare openai style key", "key sk-proj-FAKEfakeFAKEfakeFAKEfake00 refused", []string{"sk-proj-FAKEfakeFAKEfakeFAKEfake00"}, "[removed]"),
		Entry("bare hugging face token", "with hf_FAKEfakeFAKEfakeFAKEfake00 denied", []string{"hf_FAKEfakeFAKEfakeFAKEfake00"}, "[removed]"),
		Entry("bare aws access key id", "id AKIAFAKEFAKEFAKEFAKE denied", []string{"AKIAFAKEFAKEFAKEFAKE"}, "[removed]"),
		Entry("bare jwt", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmYWtlIn0.ZmFrZXNpZ25hdHVyZQ rejected", []string{"eyJzdWIiOiJmYWtlIn0", "ZmFrZXNpZ25hdHVyZQ"}, "rejected"),
	)

	DescribeTable("leaves ordinary text alone",
		func(in string) {
			Expect(sanitize.Redact(in)).To(Equal(in))
		},
		Entry("a count of tokens", "max_tokens=512 exceeded"),
		Entry("a word with a dash", "the task-runner-for-the-backend-xyz failed"),
		Entry("a word ending like a key", "mytokens are fine"),
		Entry("a path", "open models/sk-model.gguf"),
	)
})

var _ = Describe("Text", func() {
	It("puts a text with many lines on one line", func() {
		Expect(sanitize.Text("first line\n\tsecond line\r\nthird", 100)).To(Equal("first line second line third"))
	})

	It("drops control characters and terminal escapes", func() {
		out := sanitize.Text("\x1b[31mred\x1b[0m bell\x07 nul\x00 end", 100)
		Expect(out).To(Equal("red bell nul end"))
	})

	It("drops format characters that reorder or hide text", func() {
		out := sanitize.Text("file\u202egpj.exe and zero\u200bwidth \u2067x\u2069", 100)
		Expect(out).To(Equal("filegpj.exe and zerowidth x"))
	})

	It("drops markup and keeps <nil>", func() {
		out := sanitize.Text(`<script>alert(1)</script><img src=x onerror=alert(2)> failed: <nil> <b>bold</b>`, 200)
		Expect(out).NotTo(ContainSubstring("<script"))
		Expect(out).NotTo(ContainSubstring("<img"))
		Expect(out).NotTo(ContainSubstring("<b>"))
		Expect(out).To(ContainSubstring("<nil>"))
		Expect(out).To(ContainSubstring("bold"))
	})

	It("caps the length in runes, the ellipsis included, and keeps valid UTF-8", func() {
		out := sanitize.Text(strings.Repeat("é", 1000), 50)
		Expect(utf8.ValidString(out)).To(BeTrue())
		Expect(utf8.RuneCountInString(out)).To(Equal(50))
		Expect(out).To(HaveSuffix("…"))
	})

	It("is stable when it is applied again", func() {
		in := "get https://u:p@h/x?token=abc: \x1b[1m" + strings.Repeat("boom ", 300) + " open /srv/data/x.tar"
		once := sanitize.Text(in, 120)
		Expect(sanitize.Text(once, 120)).To(Equal(once))
	})

	It("removes paths, credentials and lines in one pass", func() {
		out := sanitize.Text("pull failed:\nget https://user:pass@host/v2/x: Bearer abcdef0123456789\nopen /home/alice/.config/app/key.pem", 512)
		Expect(out).NotTo(ContainSubstring("user:pass"))
		Expect(out).NotTo(ContainSubstring("abcdef0123456789"))
		Expect(out).NotTo(ContainSubstring("/home/alice"))
		Expect(out).NotTo(ContainSubstring("\n"))
		Expect(out).To(ContainSubstring("key.pem"))
	})
})

var _ = Describe("JoinLines", func() {
	DescribeTable("puts process output on one line",
		func(in, want string) {
			Expect(sanitize.JoinLines(in)).To(Equal(want))
		},
		Entry("lines", "loading\nerror: bad model\n", "loading; error: bad model"),
		Entry("crlf and blank lines", "a\r\n\r\n  b  \r\n", "a; b"),
		Entry("control characters", "a\x00b\x1b\tc", "ab\tc"),
		Entry("invalid utf-8", "a\xffb", "ab"),
		Entry("terminal colours", "\x1b[31merror\x1b[0m: bad", "error: bad"),
		Entry("bidi override and zero width characters", "a\u202eb\u200bc\u2066d\ufeff", "abcd"),
		Entry("empty", "", ""),
	)
})
