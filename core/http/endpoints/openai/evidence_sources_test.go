package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sourceURLs", func() {
	It("returns unique URLs in order of appearance, at most five", func() {
		text := "x https://1.example/a, y (https://2.example/b) z https://1.example/a " +
			"[l](https://3.example/c). \"https://4.example/d\" http://5.example/e; https://6.example/f"
		Expect(sourceURLs([]string{text})).To(Equal([]string{
			"https://1.example/a", "https://2.example/b", "https://3.example/c", "https://4.example/d", "http://5.example/e",
		}))
	})

	It("keeps the order across several tool results", func() {
		Expect(sourceURLs([]string{"https://b.example", "https://a.example https://b.example"})).
			To(Equal([]string{"https://b.example", "https://a.example"}))
	})

	It("stops at a JSON escape", func() {
		Expect(sourceURLs([]string{`"https://a.example/x\n"`})).To(Equal([]string{"https://a.example/x"}))
	})

	It("finds nothing in text without links", func() {
		Expect(sourceURLs([]string{"ftp://a.example and www.b.example"})).To(BeEmpty())
	})
})
