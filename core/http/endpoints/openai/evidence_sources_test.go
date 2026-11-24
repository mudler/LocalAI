package openai

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sourceURLs", func() {
	It("preserves parentheses inside URLs and removes surrounding Markdown punctuation", func() {
		url := "https://en.wikipedia.org/wiki/Mercury_(planet)"
		Expect(sourceURLs([]string{"[Mercury](" + url + "). (" + url + ")"})).To(Equal([]string{url}))
	})

	It("decodes multiple MCP text results before extracting URLs", func() {
		url := "https://example.com/search?a=1&b=2"
		encoded, err := json.Marshal([]string{"First " + url + "\n", "Second https://example.com/other"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sourceURLs([]string{string(encoded)})).To(Equal([]string{url, "https://example.com/other"}))
	})

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
