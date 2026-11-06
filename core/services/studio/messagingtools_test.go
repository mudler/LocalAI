package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The LocalAI groups in a chat that comes from a messaging app. The person who
// writes there may not be the owner, but the owner's own permissions still
// bound what the client tools can do, so the client group works there like in
// a Studio chat; the admin group, which changes the server, never does.
var _ = Describe("the LocalAI groups in a messaging chat", func() {
	ctx := context.Background()

	messagingChat := func(r *localaiRig, owner string, a agents.Agent) string {
		GinkgoHelper()
		a.Model = "m"
		saved, err := r.svc.SaveAgent(ctx, owner, a)
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.CreateAs(ctx, owner, studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: session.OriginConnector, ID: "binding"})
		Expect(err).NotTo(HaveOccurred())
		return rec.ID
	}

	It("offers the client group by default, as in a Studio chat", func() {
		r := newLocalAIRig(sayOK)
		id := messagingChat(r, "root", agents.Agent{Name: "Bot"})
		names := r.turnNames("root", id)
		Expect(names).To(ContainElements(clientNames()))
		g := r.group("root", id, builtins.GroupLocalAIClient)
		Expect(g.Usable).To(BeTrue())
		Expect(g.Reason).To(BeEmpty())
	})

	It("still asks the owner's permissions and models for each client tool", func() {
		r := newLocalAIRig(sayOK)
		r.set(func() { r.deniedFeats[bob] = []string{"images"} })
		names := r.turnNames(bob, messagingChat(r, bob, agents.Agent{Name: "Bot"}))
		Expect(names).NotTo(ContainElement("generate_image"))
		Expect(names).To(ContainElement("speak"))
	})

	It("leaves the client group out when the agent switches it off, and the panel says the chat cannot turn it on", func() {
		r := newLocalAIRig(sayOK)
		id := messagingChat(r, "root", agents.Agent{Name: "Quiet", BuiltinsOff: []string{builtins.GroupLocalAIClient}})
		Expect(r.turnNames("root", id)).NotTo(ContainElement("generate_image"))
		Expect(r.group("root", id, builtins.GroupLocalAIClient).Usable).To(BeFalse())
	})

	It("never offers the admin group, even when the agent switched it on and the sender is the administrator", func() {
		r := newLocalAIRig(sayOK)
		id := messagingChat(r, "root", agents.Agent{Name: "Ops", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
		names := r.turnNames("root", id)
		Expect(names).NotTo(ContainElement("install_model"))
		Expect(names).To(ContainElement("generate_image"), "the client group is unaffected")
		g := r.group("root", id, builtins.GroupLocalAIAdmin)
		Expect(g.Usable).To(BeFalse())
		Expect(g.Reason).To(ContainSubstring("messaging chat"))
		Expect(g.Reason).To(ContainSubstring("may not be the owner"))
	})

	It("gives the panel of a run that starts by itself its own reason for the admin group", func() {
		r := newLocalAIRig(sayOK)
		saved, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "Auto", Model: "m", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.CreateAs(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: session.OriginTrigger, ID: "x"})
		Expect(err).NotTo(HaveOccurred())
		g := r.group("root", rec.ID, builtins.GroupLocalAIAdmin)
		Expect(g.Usable).To(BeFalse())
		Expect(g.Reason).To(ContainSubstring("start by themselves"))
	})

	It("keeps the normal chat as it was: the client group on and the admin group opt-in", func() {
		r := newLocalAIRig(sayOK)
		names := r.names("root")
		Expect(names).To(ContainElements(clientNames()))
		Expect(names).NotTo(ContainElement("install_model"))
		Expect(r.names("root", r.adminOnFor("root"))).To(ContainElement("install_model"))
	})
})
