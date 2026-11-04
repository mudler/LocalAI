// SPDX-License-Identifier: MIT
package transport_test

import (
	"github.com/mudler/LocalAI/addons/slack/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Mrkdwn", func() {
	DescribeTable("converts Markdown to Slack markup",
		func(md, want string) { Expect(transport.Mrkdwn(md)).To(Equal(want)) },
		Entry("bold", "a **bold** word", "a *bold* word"),
		Entry("bold with underscores", "__bold__", "*bold*"),
		Entry("italic", "an *italic* word", "an _italic_ word"),
		Entry("bold and italic", "**b** and *i*", "*b* and _i_"),
		Entry("strike", "~~gone~~", "~gone~"),
		Entry("link", "see [the docs](https://x.example/a?b=1&c=2)", "see <https://x.example/a?b=1&amp;c=2|the docs>"),
		Entry("image", "![chart](https://x.example/c.png)", "<https://x.example/c.png|chart>"),
		Entry("heading", "## Steps **now**", "*Steps now*"),
		Entry("bullets", "- one\n  * two\n+ three", "• one\n  • two\n• three"),
		Entry("escapes", "a < b & c > d", "a &lt; b &amp; c &gt; d"),
		Entry("code span", "run `a **b** <c>` now", "run `a **b** &lt;c&gt;` now"),
		Entry("code block", "```go\nx := **y** < 2\n```\n**after**", "```\nx := **y** &lt; 2\n```\n*after*"),
		Entry("an asterisk in maths", "2 * 3 * 4", "2 * 3 * 4"),
		Entry("a lone backtick", "it's `odd", "it's `odd"),
	)
})

var _ = Describe("Plain", func() {
	It("drops the mention of the bot and shows links, channels and people", func() {
		Expect(transport.Plain("<@UBOT> hi <@U2|bo>, see <https://x.example|docs> and <https://y.example> in <#C1|general> &lt;b&gt; &amp; <!here>", "UBOT")).
			To(Equal("hi @U2, see docs (https://x.example) and https://y.example in #general <b> & @here"))
		Expect(transport.Plain("<@UBOT>", "UBOT")).To(BeEmpty())
	})
})

var _ = Describe("ParseTokens", func() {
	It("reads both tokens in any order and ignores the rest", func() {
		Expect(transport.ParseTokens(" xapp-1-A  xoxb-2-B junk ")).To(Equal(transport.Tokens{Bot: "xoxb-2-B", App: "xapp-1-A"}))
		Expect(transport.ParseTokens("xoxb-only")).To(Equal(transport.Tokens{Bot: "xoxb-only"}))
	})
})
