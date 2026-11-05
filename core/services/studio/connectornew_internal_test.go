package studio

import (
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the new chat command", func() {
	DescribeTable("tells a command from plain text",
		func(text string, media int, want bool) {
			m := contract.ConnectorMessage{Text: text}
			for range media {
				m.Media = append(m.Media, "f")
			}
			Expect(isNewCommand(m)).To(Equal(want))
		},
		Entry("the slash form", "/new", 0, true),
		Entry("the bang form for Slack", "!new", 0, true),
		Entry("any case", "/NEW", 0, true),
		Entry("any case, bang form", "!New", 0, true),
		Entry("white space around it", "  /new \n", 0, true),
		Entry("the name of the bot", "/new@Helper_Bot", 0, true),
		Entry("the name of the bot, lower case", "/NEW@helper_bot", 0, true),
		Entry("a bang with a bot name is not a command", "!new@Helper_Bot", 0, false),
		Entry("an empty name", "/new@", 0, false),
		Entry("text after it", "/new please summarise", 0, false),
		Entry("text before it", "please /new", 0, false),
		Entry("a longer word", "/news", 0, false),
		Entry("a longer word, bang form", "!newer", 0, false),
		Entry("a path", "/new/x", 0, false),
		Entry("a space inside", "/ new", 0, false),
		Entry("two commands", "/new /new", 0, false),
		Entry("the word alone", "new", 0, false),
		Entry("a quoted command", "\"/new\"", 0, false),
		Entry("empty", "", 0, false),
		Entry("a file with the text", "/new", 1, false),
	)
})
