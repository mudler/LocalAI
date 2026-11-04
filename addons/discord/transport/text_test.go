// SPDX-License-Identifier: MIT
package transport_test

import (
	"strings"

	"github.com/mudler/LocalAI/addons/discord/transport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the text of messages", func() {
	DescribeTable("breaks up every form of mention in an answer",
		func(in string) {
			out := transport.Escape(in)
			Expect(out).NotTo(Equal(in))
			Expect(strings.ReplaceAll(out, "​", "")).To(Equal(in))
			Expect(out).NotTo(MatchRegexp(`<@[!&]?\d+>|@everyone|@here`))
		},
		Entry("everyone", "@everyone"),
		Entry("here", "see @here now"),
		Entry("a user", "<@123456789012345678>"),
		Entry("a user with a nickname", "<@!123456789012345678>"),
		Entry("a role", "<@&123456789012345678>"),
	)

	It("leaves the rest of Markdown alone", func() {
		in := "**bold** `code` <#123456789012345678> [link](https://example.com) user@example.org"
		Expect(transport.Escape(in)).To(Equal(in))
	})

	It("removes the mention of the bot and names the people mentioned", func() {
		users := []transport.User{{ID: "1", Username: "bot"}, {ID: "2", Username: "ada", GlobalName: "Ada L"}}
		Expect(transport.Plain("<@1> ask <@!2> and <@&9>\nsecond line", users, "1")).To(Equal("ask @Ada L and <@&9>\nsecond line"))
		Expect(transport.Plain("  <@!1>  hi", users, "1")).To(Equal("hi"))
		Expect(transport.Plain("<@77> unknown", users, "1")).To(Equal("<@77> unknown"))
	})
})
