package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// streamSegmenter turns a stream of LLM token text into complete sentence/clause
// segments so TTS can start synthesizing before the full reply is generated.
var _ = Describe("streamSegmenter", func() {
	It("buffers partial text until a sentence terminator followed by space", func() {
		var s streamSegmenter
		Expect(s.Push("Hello")).To(BeEmpty())
		Expect(s.Push(" world")).To(BeEmpty())
		Expect(s.Push(". ")).To(Equal([]string{"Hello world."}))
	})

	It("emits each complete sentence and keeps the trailing partial buffered", func() {
		var s streamSegmenter
		Expect(s.Push("One. Two! Three")).To(Equal([]string{"One.", "Two!"}))
		Expect(s.Flush()).To(Equal("Three"))
	})

	It("splits on newlines", func() {
		var s streamSegmenter
		Expect(s.Push("Line one\nLine two")).To(Equal([]string{"Line one"}))
		Expect(s.Flush()).To(Equal("Line two"))
	})

	It("does not split decimals or mid-token punctuation", func() {
		var s streamSegmenter
		Expect(s.Push("Pi is 3.14 today")).To(BeEmpty())
		Expect(s.Flush()).To(Equal("Pi is 3.14 today"))
	})

	It("flushes to empty when the buffer holds only consumed text", func() {
		var s streamSegmenter
		s.Push("Done. ")
		Expect(s.Flush()).To(Equal(""))
	})
})
