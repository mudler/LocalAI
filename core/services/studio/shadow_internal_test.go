package studio

import (
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func names(tools []session.Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name+"@"+t.Provider)
	}
	return out
}

var _ = Describe("shadowing a built-in tool", func() {
	builtin := session.Tool{Name: "bash", Provider: BuiltinProvider}
	It("adds a built-in only when no add-on serves its name, and drops another tool of that name", func() {
		mcp := session.Tool{Name: "bash"} // an MCP tool that happens to be called bash
		other := session.Tool{Name: "search"}
		Expect(names(withBuiltins([]session.Tool{mcp, other}, builtin))).To(Equal([]string{"search@", "bash@builtin"}))
		sandbox := session.Tool{Name: "bash", Provider: "sandbox"}
		Expect(names(withBuiltins([]session.Tool{sandbox, other}, builtin))).To(Equal([]string{"bash@sandbox", "search@"}))
	})

	DescribeTable("keeps one add-on per shadowed name",
		func(pins map[string]string, want []string) {
			tools := []session.Tool{{Name: "bash", Provider: "zeta"}, {Name: "bash", Provider: "alpha"}, {Name: "web_fetch", Provider: "web"}, {Name: "x"}}
			Expect(names(resolveShadows(tools, pins))).To(Equal(want))
		},
		Entry("no pin: the first by ID", nil, []string{"bash@alpha", "web_fetch@web", "x@"}),
		Entry("a pin wins", map[string]string{"bash": "zeta"}, []string{"bash@zeta", "web_fetch@web", "x@"}),
		Entry("a pin to an add-on the chat lacks is ignored", map[string]string{"bash": "gone"}, []string{"bash@alpha", "web_fetch@web", "x@"}),
	)
})
