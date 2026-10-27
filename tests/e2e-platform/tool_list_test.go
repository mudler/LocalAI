package e2eplatform_test

import (
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A model provider that follows the OpenAI schema refuses a request whose tool
// list names a function twice, so a chat must hand the model each tool once.
var _ = Describe("The tool list of a turn", Label("platform", "tools"), func() {
	It("names each tool once", func() {
		in := startInstance(startOpts{Name: "toollist"})
		chat := in.api.newChat(nil)
		in.api.turn(chat, "hello")
		in.api.turn(chat, "hello again")

		picked := regexp.MustCompile(`\[pickTool\][^\n]*tools=\[([^\]]*)\]`).FindAllStringSubmatch(in.logs(), -1)
		Expect(picked).NotTo(BeEmpty(), "the debug log has no tool selection to check")
		for _, m := range picked {
			seen := map[string]bool{}
			for _, name := range strings.Fields(m[1]) {
				Expect(seen[name]).To(BeFalse(), "tool %q is offered twice: %s", name, m[0])
				seen[name] = true
			}
		}
	})
})
