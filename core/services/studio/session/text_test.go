package session_test

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("text helpers", func() {
	It("CapText cuts on a rune boundary", func() {
		s := "abé世\U0001F600" // 1+1+2+3+4 bytes
		for n := 0; n <= len(s)+2; n++ {
			got := session.CapText(s, n)
			if n < len(s) {
				Expect(len(got)).To(BeNumerically("<=", n), "CapText(%d) has %d bytes", n, len(got))
			}
			Expect(utf8.ValidString(got) && strings.HasPrefix(s, got)).To(BeTrue(), "CapText(%d) = %q", n, got)
		}
		Expect(session.CapText("short", 100)).To(Equal("short"))
		Expect(session.CapText("é", 1)).To(BeEmpty(), "a cut inside a rune must drop the rune")
		Expect(session.CapText("abc", -1)).To(BeEmpty(), "a negative n must give an empty string")
	})

	It("Summarize picks the first useful argument", func() {
		for _, tc := range []struct{ args, want string }{
			{`{"command":"ls -l","path":"x"}`, "ls -l"},
			{`{"path":"home/a.txt","content":"long"}`, "home/a.txt"},
			{`{"pattern":"*.md"}`, "*.md"},
			{`{"content":"x"}`, ""},
			{`not json`, ""},
			{``, ""},
		} {
			Expect(session.Summarize(json.RawMessage(tc.args))).To(Equal(tc.want), "Summarize(%s)", tc.args)
		}
		long := `{"command":"` + strings.Repeat("x", 500) + `"}`
		Expect(session.Summarize(json.RawMessage(long))).To(HaveLen(200), "long summary")
	})
})
