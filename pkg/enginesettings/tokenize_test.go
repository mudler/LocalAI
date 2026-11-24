package enginesettings_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	es "github.com/mudler/LocalAI/pkg/enginesettings"
)

var _ = Describe("Tokenize", func() {
	It("handles quotes and escapes without expanding anything", func() {
		toks, ops, err := es.Tokenize(`a "b c" 'd $HOME' e\ f "g\"h" "i\.j"`)
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(Equal([]string{"a", "b c", "d $HOME", "e f", `g"h`, `i\.j`}))
		Expect(ops).To(BeEmpty())
	})
	It("joins lines that end with a backslash", func() {
		toks, _, err := es.Tokenize("llama-server \\\n  -c 10 \\\n  -t 2\n")
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(Equal([]string{"llama-server", "-c", "10", "-t", "2"}))
	})
	It("reports unterminated quotes", func() {
		_, _, err := es.Tokenize(`a "b`)
		Expect(err).To(HaveOccurred())
		_, _, err = es.Tokenize(`a 'b`)
		Expect(err).To(HaveOccurred())
	})
	It("keeps operators as text and flags them", func() {
		toks, ops, err := es.Tokenize(`-c 1; rm -rf x $(id)`)
		Expect(err).ToNot(HaveOccurred())
		Expect(toks).To(ContainElement("1;"))
		Expect(toks).To(ContainElement("$(id)"))
		Expect(ops).To(HaveLen(2))
	})
})

var _ = Describe("Quote", func() {
	It("writes words that Tokenize reads back unchanged", func() {
		for _, w := range []string{"plain", "", "a b", `q"uote`, `$HOME`, "back`tick", `end\`, `mid\dle`, "it's"} {
			toks, _, err := es.Tokenize(es.Quote(w))
			Expect(err).ToNot(HaveOccurred(), w)
			Expect(toks).To(Equal([]string{w}), w)
		}
	})
})
