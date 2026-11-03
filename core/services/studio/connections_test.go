package studio_test

import (
	"context"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The connection a chat uses for the tools of an add-on that runs with a
// credential. The host picks none for the person: no first match, no default.
// The add-on in these specs is "telegram", whose bindings hold tokens; the
// rule does not depend on the platform.
var _ = Describe("the connection of a chat", func() {
	ctx := context.Background()
	const addon = "telegram"
	var (
		r     *connRig
		alpha connectors.Binding
		beta  connectors.Binding
	)
	credentialed := studio.AddonTool{Name: "addon__telegram__read", AddonID: addon, Description: "reads", Credentialed: true}

	BeforeEach(func() {
		sh := newShared()
		sh.addons.tools[alice] = append(sh.addons.tools[alice], credentialed)
		r = newConnRig(sh, connStore(), newScriptedBot(), connSay("ok"), nil)
	})
	both := func() {
		alpha = r.bind(studio.ConnectorInput{Name: "Alpha", Token: "tok-a", Resources: []string{"acme/widgets"}})
		r.sh.clock.Advance(time.Minute)
		beta = r.bind(studio.ConnectorInput{Name: "Beta", Token: "tok-b", Resources: []string{"acme/widgets"}})
	}
	newChat := func(in studio.CreateInput) string {
		in.Model = "m"
		rec, err := r.svc.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		return rec.ID
	}
	choiceOf := func(id string) studio.ConnectionChoice {
		rec, err := r.svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		c, err := r.conns.ConnectionOf(ctx, alice, rec, addon)
		Expect(err).NotTo(HaveOccurred())
		return c
	}
	offered := func(id string) bool {
		_, err := r.svc.Send(ctx, alice, id, "hi")
		Expect(err).NotTo(HaveOccurred())
		until("the tools of the turn", func() bool { return len(r.h.ToolNames(id)) > 0 })
		return slices.Contains(r.h.ToolNames(id), "addon__telegram__read")
	}
	row := func(id string) studio.AddonInfo {
		list, err := r.svc.Addons(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		i := slices.IndexFunc(list, func(a studio.AddonInfo) bool { return a.ID == addon })
		Expect(i).To(BeNumerically(">=", 0), "the credentialed add-on is listed")
		return list[i]
	}

	Describe("the resolution", func() {
		It("is none with no connection", func() {
			id := newChat(studio.CreateInput{})
			Expect(choiceOf(id).ID).To(BeEmpty())
			Expect(row(id).Connections).To(BeEmpty())
			Expect(offered(id)).To(BeFalse())
		})
		It("is the only connection, preselected", func() {
			only := r.bind(studio.ConnectorInput{Name: "Solo", Token: "t", Resources: []string{"a/b"}})
			id := newChat(studio.CreateInput{})
			c := choiceOf(id)
			Expect(c.ID).To(Equal(only.ID))
			Expect(c.By).To(Equal(studio.ConnectionOnly))
			Expect(offered(id)).To(BeTrue())
		})
		It("is none with several connections until the person picks, and the tools are not offered", func() {
			both()
			id := newChat(studio.CreateInput{})
			Expect(choiceOf(id).ID).To(BeEmpty(), "no first match and no default")
			a := row(id)
			Expect(a.Credentialed).To(BeTrue())
			Expect(a.Connections).To(HaveLen(2))
			Expect(a.Selected.ID).To(BeEmpty())
			Expect(offered(id)).To(BeFalse())
		})
		It("lists the connections oldest first", func() {
			both()
			list, err := r.conns.Connections(ctx, alice, addon)
			Expect(err).NotTo(HaveOccurred())
			Expect([]string{list[0].Name, list[1].Name}).To(Equal([]string{"Alpha", "Beta"}))
		})
		It("follows the choice of the chat and offers the tools", func() {
			both()
			id := newChat(studio.CreateInput{})
			Expect(r.svc.SetConnection(ctx, alice, id, addon, beta.ID)).To(Succeed())
			c := choiceOf(id)
			Expect(c).To(Equal(studio.ConnectionChoice{Connection: studio.Connection{ID: beta.ID, Name: "Beta"}, By: studio.ConnectionByChat}))
			Expect(offered(id)).To(BeTrue())
		})
		It("lets the agent of the chat win, and then the chat cannot change it", func() {
			both()
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Reviewer", Model: "m", Connections: map[string]string{addon: alpha.ID}})
			Expect(err).NotTo(HaveOccurred())
			id := newChat(studio.CreateInput{AgentID: ag.ID})
			c := choiceOf(id)
			Expect(c.ID).To(Equal(alpha.ID))
			Expect(c.By).To(Equal(studio.ConnectionByAgent))
			Expect(r.svc.SetConnection(ctx, alice, id, addon, beta.ID)).To(MatchError(studio.ErrConnectionByAgent))
			Expect(choiceOf(id).ID).To(Equal(alpha.ID))
		})
		It("gives an agent with no choice the same rule as a chat", func() {
			both()
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Plain", Model: "m", Addons: []string{addon}})
			Expect(err).NotTo(HaveOccurred())
			id := newChat(studio.CreateInput{AgentID: ag.ID})
			Expect(choiceOf(id).ID).To(BeEmpty())
			Expect(r.svc.SetConnection(ctx, alice, id, addon, alpha.ID)).To(Succeed())
			Expect(choiceOf(id).ID).To(Equal(alpha.ID))
		})
		It("counts a deleted connection as none, with no fallback", func() {
			both()
			id := newChat(studio.CreateInput{})
			Expect(r.svc.SetConnection(ctx, alice, id, addon, alpha.ID)).To(Succeed())
			Expect(r.conns.DeleteConnector(ctx, alice, alpha.ID)).To(Succeed())
			Expect(choiceOf(id).ID).To(Equal(beta.ID), "one is left, so it is the only connection")
			third := r.bind(studio.ConnectorInput{Name: "Gamma", Token: "t3"})
			Expect(third.ID).NotTo(BeEmpty())
			Expect(choiceOf(id).ID).To(BeEmpty(), "the stale choice is not replaced by a first match")
		})
		It("uses the binding that started the chat", func() {
			both()
			rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: "connector", ID: beta.ID})
			Expect(err).NotTo(HaveOccurred())
			Expect(choiceOf(rec.ID).By).To(Equal(studio.ConnectionByBinding))
			Expect(choiceOf(rec.ID).ID).To(Equal(beta.ID))
		})
		It("keeps the connections of one person from another", func() {
			both()
			c, err := r.conns.Connections(ctx, "mallory", addon)
			Expect(err).NotTo(HaveOccurred())
			Expect(c).To(BeEmpty())
		})
	})

	Describe("choosing", func() {
		BeforeEach(both)
		It("refuses a connection that is not the person's own for that add-on, and an add-on ID that is not one", func() {
			id := newChat(studio.CreateInput{})
			Expect(r.svc.SetConnection(ctx, alice, id, addon, "nope")).To(MatchError(studio.ErrBadConnection))
			Expect(r.svc.SetConnection(ctx, alice, id, "slack", alpha.ID)).To(MatchError(studio.ErrBadConnection))
			Expect(r.svc.SetConnection(ctx, alice, id, "Not Valid", alpha.ID)).To(MatchError(studio.ErrBadAddonID))
		})
		It("belongs to the owner of the chat", func() {
			id := newChat(studio.CreateInput{})
			Expect(r.svc.SetConnection(ctx, "mallory", id, addon, alpha.ID)).NotTo(Succeed())
		})
		It("switches the add-on on and can be cleared", func() {
			id := newChat(studio.CreateInput{Switches: studio.Switches{AddonsOff: []string{addon}}})
			Expect(r.svc.SetConnection(ctx, alice, id, addon, alpha.ID)).To(Succeed())
			rec, _ := r.svc.Get(ctx, alice, id)
			Expect(rec.AddonsOff).NotTo(ContainElement(addon))
			Expect(r.svc.SetConnection(ctx, alice, id, addon, "")).To(Succeed())
			Expect(choiceOf(id).ID).To(BeEmpty())
		})
		It("is stored with a new chat, like the other switches, and checked", func() {
			id := newChat(studio.CreateInput{Switches: studio.Switches{Connections: map[string]string{addon: beta.ID}}})
			Expect(choiceOf(id).ID).To(Equal(beta.ID))
			_, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", Switches: studio.Switches{Connections: map[string]string{addon: "nope"}}})
			Expect(err).To(MatchError(studio.ErrBadConnection))
		})
		It("shows in a draft, and a draft that names a gone connection shows none", func() {
			list, _, err := r.svc.DraftAddons(ctx, alice, "m", "", studio.Switches{Connections: map[string]string{addon: beta.ID}})
			Expect(err).NotTo(HaveOccurred())
			i := slices.IndexFunc(list, func(a studio.AddonInfo) bool { return a.ID == addon })
			Expect(list[i].Selected.ID).To(Equal(beta.ID))
			list, _, err = r.svc.DraftAddons(ctx, alice, "m", "", studio.Switches{Connections: map[string]string{addon: "gone"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(list[i].ID).To(Equal(addon))
			Expect(list[slices.IndexFunc(list, func(a studio.AddonInfo) bool { return a.ID == addon })].Selected.ID).To(BeEmpty())
		})
		It("goes to a fork of the chat", func() {
			id := newChat(studio.CreateInput{})
			Expect(r.svc.SetConnection(ctx, alice, id, addon, beta.ID)).To(Succeed())
			fork, err := r.svc.Fork(ctx, alice, id, 0, "copy")
			Expect(err).NotTo(HaveOccurred())
			Expect(choiceOf(fork.ID).ID).To(Equal(beta.ID))
		})
	})

	Describe("an agent profile", func() {
		BeforeEach(both)
		It("accepts only the person's own connection for the add-on", func() {
			_, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Bad", Model: "m", Connections: map[string]string{addon: "nope"}})
			Expect(err).To(MatchError(studio.ErrBadConnection))
		})
		It("keeps its connections when a write does not mention them, and clears them with an empty map", func() {
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Keeper", Model: "m", Connections: map[string]string{addon: alpha.ID}})
			Expect(err).NotTo(HaveOccurred())
			again, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Keeper 2", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			Expect(again.Connections).To(Equal(map[string]string{addon: alpha.ID}))
			cleared, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Keeper 2", Model: "m", Connections: map[string]string{}})
			Expect(err).NotTo(HaveOccurred())
			Expect(cleared.Connections).To(BeEmpty())
		})
	})
})
