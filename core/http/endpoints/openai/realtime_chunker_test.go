package openai

import (
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// clauseChunker splits streamed LLM content into speakable clauses in a
// script-aware way: UAX#29 sentences (Latin .!? and CJK 。！？), CJK clause
// punctuation, and Thai/Lao spaces — never whitespace-splitting CJK.
var _ = Describe("clauseChunker", func() {
	Context("Latin sentences", func() {
		It("emits a sentence only once following content confirms it is complete", func() {
			c := newClauseChunker(12, 200)
			Expect(c.push("Hello world. How are you?")).To(Equal([]string{"Hello world."}))
			// The trailing sentence is held until flush (the next token might extend it).
			Expect(c.flush()).To(Equal([]string{"How are you?"}))
		})

		It("assembles a sentence across many small tokens", func() {
			c := newClauseChunker(12, 200)
			var got []string
			for _, tok := range []string{"Hello", " world.", " How", " are", " you?"} {
				got = append(got, c.push(tok)...)
			}
			got = append(got, c.flush()...)
			Expect(got).To(Equal([]string{"Hello world.", "How are you?"}))
		})

		It("does not split decimals or abbreviations (UAX#29 SB6)", func() {
			c := newClauseChunker(12, 200)
			got := c.push("Pi is 3.14 and e is 2.72. Done")
			Expect(got).To(Equal([]string{"Pi is 3.14 and e is 2.72."}))
			Expect(c.flush()).To(Equal([]string{"Done"}))
		})
	})

	Context("CJK (no whitespace)", func() {
		It("splits Chinese on the ideographic full stop", func() {
			c := newClauseChunker(12, 200)
			Expect(c.push("你好世界。今天天气很好。")).To(Equal([]string{"你好世界。"}))
			Expect(c.flush()).To(Equal([]string{"今天天气很好。"}))
		})

		It("splits Japanese on the ideographic full stop", func() {
			c := newClauseChunker(12, 200)
			Expect(c.push("こんにちは。元気ですか。")).To(Equal([]string{"こんにちは。"}))
			Expect(c.flush()).To(Equal([]string{"元気ですか。"}))
		})

		It("splits on CJK clause punctuation for lower latency", func() {
			c := newClauseChunker(2, 200) // small min so short test clauses cut
			Expect(c.push("你好，世界。再见")).To(Equal([]string{"你好，", "世界。"}))
			Expect(c.flush()).To(Equal([]string{"再见"}))
		})
	})

	Context("Thai (spaces mark clauses, not words)", func() {
		It("splits a Thai run on the inter-clause space", func() {
			c := newClauseChunker(2, 200)
			Expect(c.push("สวัสดีครับ กินข้าวไหม")).To(Equal([]string{"สวัสดีครับ"}))
			Expect(c.flush()).To(Equal([]string{"กินข้าวไหม"}))
		})

		It("never shatters a space-less Thai run into characters", func() {
			c := newClauseChunker(2, 200)
			Expect(c.push("สวัสดีครับ")).To(BeEmpty()) // held, no boundary
			Expect(c.flush()).To(Equal([]string{"สวัสดีครับ"}))
		})
	})

	Context("length cap (UAX#14 fallback)", func() {
		It("force-breaks an over-long punctuation-less CJK run at legal points", func() {
			c := newClauseChunker(4, 10) // maxRunes = 10
			run := strings.Repeat("字", 25)
			got := c.push(run)
			got = append(got, c.flush()...)
			total := 0
			for _, seg := range got {
				n := utf8.RuneCountInString(seg)
				Expect(n).To(BeNumerically("<=", 10)) // never exceeds the cap
				total += n
			}
			Expect(total).To(Equal(25))                       // nothing dropped
			Expect(len(got)).To(BeNumerically(">=", 3))       // 10 + 10 + 5
		})
	})

	Context("buffer lifecycle", func() {
		It("flush clears the buffer so the chunker is reusable", func() {
			c := newClauseChunker(12, 200)
			// "First one." is confirmed by the following "Second", so push drains it;
			// only the unterminated tail remains for flush.
			Expect(c.push("First one. Second")).To(Equal([]string{"First one."}))
			Expect(c.flush()).To(Equal([]string{"Second"}))
			Expect(c.flush()).To(BeEmpty())
			Expect(c.push("Again. More")).To(Equal([]string{"Again."}))
		})
	})
})
