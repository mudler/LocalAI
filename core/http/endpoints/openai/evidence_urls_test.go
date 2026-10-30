package openai

import (
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("urlKey", func() {
	DescribeTable("normalises links that name the same page to one key",
		func(raw, want string) { Expect(urlKey(raw)).To(Equal(want)) },
		Entry("drops the scheme", "https://example.com/a", "example.com/a"),
		Entry("treats http and https alike", "http://example.com/a", "example.com/a"),
		Entry("drops www. and lower-cases the host", "https://WWW.Example.com/A", "example.com/A"),
		Entry("drops a trailing slash", "https://example.com/a/", "example.com/a"),
		Entry("drops the fragment", "https://example.com/a#section", "example.com/a"),
		Entry("keeps the query", "https://example.com/a/?q=1#x", "example.com/a?q=1"),
		Entry("reduces a bare host", "https://example.com/", "example.com"),
	)
})

var _ = Describe("unverifiedURLs", func() {
	request := func(texts ...string) map[string]struct{} {
		return evidenceURLs("", userMessages(texts...))
	}

	DescribeTable("lists the links of the answer that no evidence backs",
		func(answer string, req map[string]struct{}, toolResults []string, want []string) {
			got := unverifiedURLs(answer, req, toolResults)
			if want == nil {
				Expect(got).To(BeEmpty())
			} else {
				Expect(got).To(Equal(want))
			}
		},
		Entry("an invented link",
			"See https://www.statista.com/statistics/123/market for details.",
			request("How big is the market?"), nil,
			[]string{"https://www.statista.com/statistics/123/market"}),
		Entry("a link a tool returned",
			"See https://a.example/oil.",
			request("Is this oil good?"), []string{`{"url":"https://a.example/oil"}`},
			nil),
		Entry("a link from the user message",
			"The page https://shop.example/oil says so.",
			request("Is https://shop.example/oil a good olive oil?"), nil,
			nil),
		Entry("a Markdown link that was not retrieved",
			"According to [Gartner](https://www.gartner.com/en/newsroom/press-releases/2026).",
			request("What do analysts say?"), []string{"https://a.example/oil"},
			[]string{"https://www.gartner.com/en/newsroom/press-releases/2026"}),
		Entry("a Markdown link a tool returned",
			"According to [the test](https://a.example/oil).",
			request("Is it good?"), []string{"Result: https://a.example/oil"},
			nil),
		Entry("trailing punctuation, slash, fragment and www. do not matter",
			"Read https://www.a.example/oil/#top, then (https://b.example/test).",
			request("?"), []string{"https://a.example/oil and https://b.example/test/"},
			nil),
		Entry("the site of a retrieved page counts as retrieved",
			"Source: https://a.example",
			request("?"), []string{"https://a.example/reports/2026/oil.pdf"},
			nil),
		Entry("a deeper path than the retrieved page does not",
			"Source: https://a.example/reports/2026/other.pdf",
			request("?"), []string{"https://a.example/reports/2026/oil.pdf"},
			[]string{"https://a.example/reports/2026/other.pdf"}),
		Entry("a host that only starts like a retrieved one does not",
			"Source: https://a.ex",
			request("?"), []string{"https://a.example/oil"},
			[]string{"https://a.ex"}),
		Entry("a link escaped by a JSON encoder in the tool result",
			"Source: https://a.example/oil",
			request("?"), []string{`{"url":"https:\/\/a.example\/oil"}`},
			nil),
		Entry("each unbacked link once, at most five",
			"https://1.example https://2.example https://1.example/ https://3.example https://4.example https://5.example https://6.example",
			request("?"), nil,
			[]string{"https://1.example", "https://2.example", "https://3.example", "https://4.example", "https://5.example"}),
		Entry("an answer without links",
			"No link here.", request("?"), nil, nil),
	)

	It("does not take a link from an earlier assistant turn as evidence", func() {
		msgs := []schema.Message{
			{Role: "user", Content: "What do analysts say?", StringContent: "What do analysts say?"},
			{Role: "assistant", Content: "See https://gartner.example/x", StringContent: "See https://gartner.example/x"},
			{Role: "user", Content: "And?", StringContent: "And?"},
		}
		Expect(unverifiedURLs("Again https://gartner.example/x", evidenceURLs("", msgs), nil)).
			To(Equal([]string{"https://gartner.example/x"}))
	})

	It("takes the system prompt and client tool messages as evidence", func() {
		msgs := []schema.Message{
			{Role: "tool", Content: "https://tool.example/r", StringContent: "https://tool.example/r"},
		}
		Expect(unverifiedURLs("https://sys.example/a and https://tool.example/r", evidenceURLs("Use https://sys.example/a", msgs), nil)).
			To(BeEmpty())
	})
})
