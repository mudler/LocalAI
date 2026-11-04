package builtins_test

import (
	"context"
	"encoding/json"
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

	It("leaves an External tool to its builder and names it hidden when its group is off", func() {
		r := builtins.NewRegistry()
		Expect(r.Register(builtins.Tool{ID: "read_file", External: true, Unlisted: true, Build: build})).NotTo(Succeed(), "an external tool needs a group")
		Expect(r.Register(builtins.Tool{ID: "read_file", Group: builtins.GroupFiles, External: true, Unlisted: true, Build: build})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "web_fetch", Group: builtins.GroupWeb, Setting: "s", Replaceable: true, Build: build})).To(Succeed())
		Expect(r.Build(context.Background(), builtins.Chat{})).To(HaveLen(1), "the external tool is not built")
		Expect(r.GroupOf("read_file")).To(Equal(builtins.GroupFiles))
		Expect(r.Hidden(builtins.Chat{})).To(BeEmpty())
		Expect(r.Hidden(builtins.Chat{GroupsOff: []string{builtins.GroupFiles, builtins.GroupWeb}})).To(Equal(map[string]bool{"read_file": true}),
			"web_fetch is built by the registry, and an add-on's web_fetch keeps its own switch")
	})

	It("hides the replacement of a Covers tool with its group", func() {
		r := builtins.NewRegistry()
		Expect(r.Register(builtins.Tool{ID: "bash", Setting: "s", Covers: true, Replaceable: true, Build: build})).NotTo(Succeed(), "covers needs a group")
		Expect(r.Register(builtins.Tool{ID: "bash", Group: builtins.GroupShell, Setting: "s", Covers: true, Build: build})).NotTo(Succeed(), "covers needs a replaceable tool")
		Expect(r.Register(builtins.Tool{ID: "bash", Group: builtins.GroupShell, Setting: "s", Covers: true, Replaceable: true, Build: build})).To(Succeed())
		Expect(r.Hidden(builtins.Chat{GroupsOff: []string{builtins.GroupShell}})).To(Equal(map[string]bool{"bash": true}))
		got := r.Build(context.Background(), builtins.Chat{GroupsOff: []string{builtins.GroupShell}})
		Expect(got[0].Reason).To(Equal(builtins.ReasonChatOff))
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

var _ = Describe("a tool gated by a capability", func() {
	var held map[string]bool
	var audited []string
	chat := func() builtins.Chat {
		return builtins.Chat{
			Origin: "connector",
			Allow: func(c string) (bool, string) {
				if held[c] {
					return true, ""
				}
				return false, "no " + c
			},
			Audit: func(c string, ok bool, _ string) { audited = append(audited, c) },
		}
	}
	register := func() *builtins.Registry {
		r := builtins.NewRegistry()
		ran := func(context.Context, builtins.Chat) (session.Tool, bool, string) {
			return session.Tool{Description: "x", Run: func(context.Context, json.RawMessage) (string, error) { return "ran", nil }}, true, ""
		}
		Expect(r.Register(builtins.Tool{ID: "gated", Unlisted: true, Capabilities: []string{"memory.write"}, Build: ran})).To(Succeed())
		Expect(r.Register(builtins.Tool{ID: "open", Unlisted: true, Build: ran})).To(Succeed())
		return r
	}
	BeforeEach(func() { held, audited = map[string]bool{}, nil })

	It("refuses a capability that is not in the closed list", func() {
		err := builtins.NewRegistry().Register(builtins.Tool{ID: "x", Unlisted: true, Capabilities: []string{"shell.run"}, Build: func(context.Context, builtins.Chat) (session.Tool, bool, string) { return session.Tool{}, true, "" }})
		Expect(err).To(HaveOccurred())
	})
	It("is not offered to a sender without the capability, and the others are", func() {
		got := register().Build(context.Background(), chat())
		Expect(got[0].OK).To(BeFalse())
		Expect(got[0].Reason).To(HavePrefix(builtins.ReasonNotPermitted))
		Expect(got[1].OK).To(BeTrue())
	})
	It("is offered to a chat that has no sender check", func() {
		got := register().Build(context.Background(), builtins.Chat{})
		Expect(got[0].OK).To(BeTrue())
	})
	It("is refused at the call when the capability went away after the tool was offered", func() {
		held["memory.write"] = true
		got := register().Build(context.Background(), chat())
		Expect(got[0].OK).To(BeTrue())
		out, err := got[0].Tool.Run(context.Background(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("ran"))
		held["memory.write"] = false
		_, err = got[0].Tool.Run(context.Background(), nil)
		Expect(err).To(MatchError(ContainSubstring("not permitted: no memory.write")))
		Expect(audited).To(Equal([]string{"memory.write"}))
	})
})

var _ = Describe("the files.upload grant", func() {
	// The connector gate enforces files.upload on the files a sender attaches to
	// a message (the sender's files going to the agent). The text of the editor
	// must say that, not the opposite direction.
	It("says that senders' files reach the agent, and not that the agent sends files", func() {
		g, ok := builtins.LookupGrant(builtins.GrantFilesUpload)
		Expect(ok).To(BeTrue())
		Expect(g.Title).To(Equal("Receive files"))
		Expect(g.Description).To(ContainSubstring("attach files for the agent to read"))
		Expect(g.Title + g.Description).NotTo(ContainSubstring("from your folders"))
		Expect(g.Sensitive).To(BeFalse())
	})
})
