package studio_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// describedAddons is a broker that also says what its add-ons declare.
type describedAddons struct {
	*fakeAddons
	listings []studio.AddonListing
}

func (l describedAddons) AddonListings(context.Context, string) ([]studio.AddonListing, error) {
	return l.listings, nil
}

var _ = Describe("what a person reads next to a tool switch", func() {
	It("gives every listed group a title and a description of what the model can do", func() {
		svc, _ := newGroupsChat(nil)
		id := create(svc, alice)
		p, err := svc.Tools(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Builtins).NotTo(BeEmpty())
		for _, g := range p.Builtins {
			Expect(g.Title).NotTo(BeEmpty(), g.Group)
			Expect(g.Description).To(HavePrefix("The model can"), g.Group)
			Expect(g.WhenOff).NotTo(BeEmpty(), g.Group)
		}
		// The group with no tool in the registry list of the interface
		// ("changes") reads as a title, not as its ID.
		changes := builtinGroup(p.Builtins, builtins.GroupChanges)
		Expect(changes.Title).To(Equal("Suggested changes"))
		Expect(changes.Description).To(ContainSubstring("until you confirm"))
	})

	It("falls back to the first tool of a group that nobody described", func() {
		r := builtins.NewRegistry()
		Expect(r.Register(builtins.Tool{ID: "x_tool", Group: "x", Label: "X tool", Description: "Does x.", Unlisted: true,
			Build: func(context.Context, builtins.Chat) (session.Tool, bool, string) { return session.Tool{}, true, "" }})).To(Succeed())
		Expect(r.GroupInfo("x")).To(Equal(builtins.GroupInfo{Title: "X tool", Description: "Does x."}))
		Expect(r.GroupInfo("nothing")).To(Equal(builtins.GroupInfo{}))
		r.DescribeGroup("y", builtins.GroupInfo{Title: "Y", Description: "The model can y."})
		Expect(r.GroupInfo("y").Title).To(Equal("Y"))
	})

	It("lists the title and the description an add-on declares", func() {
		broker := describedAddons{&fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}},
			[]studio.AddonListing{{AddonID: "sandbox", Title: "Sandbox", Description: "Runs code in isolation."}}}
		svc, _ := newGroupsChat(broker)
		id := create(svc, alice)
		p, err := svc.Tools(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Addons).NotTo(BeEmpty())
		Expect(p.Addons[0].Title).To(Equal("Sandbox"))
		Expect(p.Addons[0].Description).To(Equal("Runs code in isolation."))
	})
})

var _ = Describe("the tool switches of a saved agent", func() {
	newAgentChat := func() (*studio.Service, agents.Agent) {
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, _ := newGroupsChat(nil, func(d *studio.Deps) { d.Agents = ag })
		a, err := svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Careful", BuiltinsOff: []string{"files", "nope"}, BuiltinsOn: []string{"saved_agents"}})
		Expect(err).NotTo(HaveOccurred())
		return svc, a
	}

	It("keeps the groups the server has, and keeps them when a write does not mention them", func() {
		svc, a := newAgentChat()
		Expect(a.BuiltinsOff).To(Equal([]string{"files"}))
		again, err := svc.SaveAgent(context.Background(), alice, agents.Agent{ID: a.ID, Name: "Careful 2"})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.BuiltinsOff).To(Equal([]string{"files"}), "an older client cannot widen the agent")
		cleared, err := svc.SaveAgent(context.Background(), alice, agents.Agent{ID: a.ID, Name: "Careful 2", BuiltinsOff: []string{}, BuiltinsOn: []string{}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cleared.BuiltinsOff).To(BeEmpty())
	})

	It("is a ceiling: a chat of the agent cannot switch the group on, and the draft says why", func() {
		svc, a := newAgentChat()
		rec, err := svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: a.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.BuiltinsOn).To(ContainElement("saved_agents"), "the agent starts with saved agents on")
		Expect(svc.SetBuiltinsOff(context.Background(), alice, rec.ID, []string{})).To(Succeed())
		for _, p := range []studio.ToolsPanel{mustTools(svc, rec.ID), mustDraft(svc, a.ID)} {
			files := builtinGroup(p.Builtins, "files")
			Expect(files.Off).To(BeTrue())
			Expect(files.Usable).To(BeFalse())
			Expect(files.Reason).To(Equal(studio.ReasonOffForAgent))
			Expect(builtinGroup(p.Builtins, "media").Off).To(BeFalse())
		}
	})
})

func mustTools(svc *studio.Service, id string) studio.ToolsPanel {
	p, err := svc.Tools(context.Background(), alice, id)
	Expect(err).NotTo(HaveOccurred())
	return p
}

func mustDraft(svc *studio.Service, agentID string) studio.ToolsPanel {
	p, err := svc.DraftTools(context.Background(), alice, "m", agentID)
	Expect(err).NotTo(HaveOccurred())
	return p
}
