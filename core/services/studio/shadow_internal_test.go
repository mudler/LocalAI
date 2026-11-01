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
			Expect(names(resolveShadows(tools, pins, nil, shadowers{}))).To(Equal(want))
		},
		Entry("no pin: the first by ID", nil, []string{"bash@alpha", "web_fetch@web", "x@"}),
		Entry("a pin wins", map[string]string{"bash": "zeta"}, []string{"bash@zeta", "web_fetch@web", "x@"}),
		Entry("a pin to an add-on the chat lacks is ignored", map[string]string{"bash": "gone"}, []string{"bash@alpha", "web_fetch@web", "x@"}),
	)

	DescribeTable("picks the default provider: the oldest install",
		func(installed map[string]string, pin string, want string, order []string) {
			ids := []string{"zeta", "mid", "alpha"}
			Expect(ChooseProvider(ids, installed, pin)).To(Equal(want))
			Expect(OrderProviders(ids, installed)).To(Equal(order))
		},
		Entry("the oldest wins over a smaller ID", map[string]string{"zeta": "2026-01-01T00:00:00Z", "mid": "2026-02-01T00:00:00Z", "alpha": "2026-03-01T00:00:00Z"}, "", "zeta", []string{"zeta", "mid", "alpha"}),
		Entry("equal times go by ID", map[string]string{"zeta": "2026-01-01T00:00:00Z", "mid": "2026-01-01T00:00:00Z", "alpha": "2026-01-01T00:00:00Z"}, "", "alpha", []string{"alpha", "mid", "zeta"}),
		Entry("no times at all go by ID", nil, "", "alpha", []string{"alpha", "mid", "zeta"}),
		Entry("a record with no time sorts after the ones with a time", map[string]string{"zeta": "2026-05-01T00:00:00Z"}, "", "zeta", []string{"zeta", "alpha", "mid"}),
		Entry("an unreadable time counts as none", map[string]string{"mid": "yesterday", "zeta": "2026-05-01T00:00:00Z"}, "", "zeta", []string{"zeta", "alpha", "mid"}),
		Entry("a pin wins", map[string]string{"zeta": "2026-01-01T00:00:00Z"}, "mid", "mid", []string{"zeta", "alpha", "mid"}),
		Entry("a pin to an add-on that is not a candidate is ignored", map[string]string{"mid": "2026-01-01T00:00:00Z"}, "gone", "mid", []string{"mid", "alpha", "zeta"}),
	)

	It("keeps the oldest add-on tool of a shadowed name when nothing is pinned", func() {
		tools := []session.Tool{{Name: "bash", Provider: "alpha"}, {Name: "bash", Provider: "zeta"}, {Name: "x"}}
		installed := map[string]string{"alpha": "2026-03-01T00:00:00Z", "zeta": "2026-01-01T00:00:00Z"}
		Expect(names(resolveShadows(tools, nil, installed, shadowers{}))).To(Equal([]string{"bash@zeta", "x@"}))
		Expect(names(resolveShadows(tools, map[string]string{"bash": "alpha"}, installed, shadowers{}))).To(Equal([]string{"bash@alpha", "x@"}))
	})
})
