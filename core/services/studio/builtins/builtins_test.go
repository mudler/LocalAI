package builtins_test

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

func TestBuiltins(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Built-in tools Suite")
}

var _ = Describe("the registry of built-in tools", func() {
	build := func(context.Context, builtins.Chat) (session.Tool, bool, string) {
		return session.Tool{Description: "x"}, true, ""
	}
	It("takes a tool once, with a Build and a Setting, and a replaceable one only by a replaceable name", func() {
		r := builtins.NewRegistry()
		Expect(r.Register(builtins.Tool{ID: "bash", Setting: "s", Replaceable: true, Build: build})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "bash", Setting: "s", Build: build})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{ID: "write_file", Setting: "s", Replaceable: true, Build: build})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch", Setting: "s"})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch", Build: build})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{Setting: "s", Build: build})).NotTo(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_search", Setting: "s", Replaceable: true, Build: build})).To(Succeed())
		var ids []string
		for _, t := range r.Tools() {
			ids = append(ids, t.ID)
		}
		Expect(ids).To(Equal([]string{"bash", "web_search"}), "in the order of registration")
	})

	It("builds the tools that are on, named by ID with the built-in provider", func() {
		r := builtins.NewRegistry()
		on := false
		Expect(r.Register(builtins.Tool{ID: "bash", Setting: "s", Build: build, Enabled: func() bool { return on }})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch", Setting: "s", Build: func(context.Context, builtins.Chat) (session.Tool, bool, string) {
			return session.Tool{}, false, "no model"
		}})).To(Succeed())
		got := r.Build(context.Background(), builtins.Chat{})
		Expect(got).To(HaveLen(2))
		Expect(got[0]).To(MatchFields(IgnoreExtras, Fields{"ID": Equal("bash"), "OK": BeFalse(), "Reason": Equal(builtins.ReasonOff)}))
		Expect(got[1].Reason).To(Equal("no model"))
		on = true
		got = r.Build(context.Background(), builtins.Chat{})
		Expect(got[0].OK).To(BeTrue())
		Expect(got[0].Tool.Name).To(Equal("bash"))
		Expect(got[0].Tool.Provider).To(Equal(builtins.BuiltinProvider))
	})

	It("knows the switch groups and leaves out the tools of a group the chat switched off", func() {
		r := builtins.NewRegistry()
		Expect(r.Register(builtins.Tool{ID: "bash", Setting: "s", Build: build})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_search", Group: builtins.GroupWeb, Setting: "s", Options: []string{"o"}, Build: build})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch", Group: builtins.GroupWeb, Setting: "s", Build: build})).To(Succeed())
		Expect(r.Groups()).To(Equal([]string{"web"}))
		Expect(r.IsGroup("web")).To(BeTrue())
		Expect(r.IsGroup("bash")).To(BeFalse())
		Expect(r.IsGroup("")).To(BeFalse())

		got := r.Build(context.Background(), builtins.Chat{GroupsOff: []string{"web"}})
		Expect(got).To(HaveLen(3))
		Expect(got[0].OK).To(BeTrue())
		Expect(got[1]).To(MatchFields(IgnoreExtras, Fields{"ID": Equal("web_search"), "OK": BeFalse(), "Reason": Equal(builtins.ReasonChatOff)}))
		Expect(got[2].Reason).To(Equal(builtins.ReasonChatOff))
	})

	It("keeps an opt-in group off until the chat switches it on", func() {
		r := builtins.NewRegistry()
		Expect(r.Register(builtins.Tool{ID: "list_agents", Setting: "s", OptIn: true, Build: build})).NotTo(Succeed(), "an opt-in tool needs a group")
		Expect(r.Register(builtins.Tool{ID: "list_agents", Group: builtins.GroupSavedAgents, Setting: "s", OptIn: true, Build: build})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch", Group: builtins.GroupWeb, Setting: "s", Build: build})).To(Succeed())
		Expect(r.IsOptIn(builtins.GroupSavedAgents)).To(BeTrue())
		Expect(r.IsOptIn(builtins.GroupWeb)).To(BeFalse())

		got := r.Build(context.Background(), builtins.Chat{})
		Expect(got[0]).To(MatchFields(IgnoreExtras, Fields{"ID": Equal("list_agents"), "OK": BeFalse(), "Reason": Equal(builtins.ReasonNotOn)}))
		Expect(got[1].OK).To(BeTrue())

		got = r.Build(context.Background(), builtins.Chat{GroupsOn: []string{builtins.GroupSavedAgents}})
		Expect(got[0].OK).To(BeTrue())

		got = r.Build(context.Background(), builtins.Chat{GroupsOn: []string{builtins.GroupSavedAgents}, GroupsOff: []string{builtins.GroupSavedAgents}})
		Expect(got[0].Reason).To(Equal(builtins.ReasonChatOff), "off wins over on")
	})

	It("hands out copies", func() {
		r := builtins.NewRegistry()
		opts := []string{"o"}
		Expect(r.Register(builtins.Tool{ID: "web_search", Setting: "s", Options: opts, Build: build})).To(Succeed())
		opts[0] = "changed"
		a := r.Tools()
		Expect(a[0].Options).To(Equal([]string{"o"}))
		a[0].Options[0] = "changed"
		t, ok := r.Lookup("web_search")
		Expect(ok).To(BeTrue())
		Expect(t.Options).To(Equal([]string{"o"}))
	})
})
