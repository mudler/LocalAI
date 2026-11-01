// SPDX-License-Identifier: MIT
package transport_test

import (
	"encoding/json"
	"os"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("MarkdownV2",
	func(md, want string) { Expect(transport.MarkdownV2(md)).To(Equal(want)) },
	Entry("headings, bold, italics, a link with parentheses and a code block",
		"# Heading\n\n**bold** and *italic* with [link](https://example.com/a_(b)).\n\n```go\nfmt.Println(`ok`)\n```",
		"*Heading*\n\n*bold* and _italic_ with [link](https://example.com/a_\\(b\\))\\.\n\n```go\nfmt.Println(\\`ok\\`)\n```"),
	Entry("inline code keeps its text and escapes only the backslash",
		"Use `a\\b` and `x` now.", "Use `a\\\\b` and `x` now\\."),
	Entry("two links on a line stay two links",
		"[a](https://a.example) or [b](https://b.example/x)", "[a](https://a.example) or [b](https://b.example/x)"),
	Entry("a list item opens no italics", "* one item\n* two items", "\\* one item\n\\* two items"),
	Entry("code inside bold", "**run `make`**", "*run `make`*"),
	Entry("a heading drops the bold inside it", "## **Title**", "*Title*"),
	Entry("every reserved character of plain text", "a_b*c[d]e(f)g~h>i#j+k-l=m|n{o}p.q!r\\s",
		"a\\_b\\*c\\[d\\]e\\(f\\)g\\~h\\>i\\#j\\+k\\-l\\=m\\|n\\{o\\}p\\.q\\!r\\\\s"),
)

var _ = DescribeTable("PlainText",
	func(md, want string) { Expect(transport.PlainText(md)).To(Equal(want)) },
	Entry("markup goes, a link keeps its address", "# Heading\n\n**bold** and [link](https://example.com)",
		"Heading\n\nbold and link (https://example.com)"),
	Entry("a code block keeps its code, not its fences or language", "see:\n```go\nx := 1\n```\ndone", "see:\nx := 1\ndone"),
)

var _ = Describe("the manifest", func() {
	It("declares drafts and the preview interval of the kind", func() {
		raw, err := os.ReadFile("../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		var m contract.Manifest
		Expect(json.Unmarshal(raw, &m)).To(Succeed())
		k := m.Capabilities.Connector.Kinds[0]
		Expect(k.ID).To(Equal(transport.Kind))
		Expect(k.Drafts).To(BeTrue())
		Expect(k.PreviewMs).To(Equal(400))
	})
})
