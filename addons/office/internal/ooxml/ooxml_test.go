package ooxml_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/office/internal/ooxml"
)

var _ = Describe("Escape", func() {
	It("escapes the XML special characters", func() {
		Expect(ooxml.Escape(`a<b>&"c'`)).To(Equal("a&lt;b&gt;&amp;&#34;c&#39;"))
	})

	It("keeps plain text as it is", func() {
		Expect(ooxml.Escape("plain text")).To(Equal("plain text"))
	})
})

var _ = Describe("HasDotDot", func() {
	DescribeTable("finds a parent segment with either separator",
		func(name string, want bool) {
			Expect(ooxml.HasDotDot(name)).To(Equal(want))
		},
		Entry("plain part", "word/document.xml", false),
		Entry("dots in a name", "word/a..b.xml", false),
		Entry("slash parent", "word/../x.xml", true),
		Entry("backslash parent", `word\..\x.xml`, true),
		Entry("leading parent", "../x.xml", true),
		Entry("only parent", "..", true),
	)
})
