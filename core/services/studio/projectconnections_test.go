package studio_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	projectBinding = "a project connection can only use agents of its project"
	plainBinding   = "a connection that is not in a project cannot use a project agent"
)

var _ = Describe("the connections of a project", func() {
	var (
		r                         *projRig
		ctx                       context.Context
		acme, other               projects.Project
		inA, inA2, inOther, plain agents.Agent
	)
	BeforeEach(func() {
		r = newProjRig()
		ctx = r.ctx
		acme, other = r.project("Acme"), r.project("Other")
		inA, inA2, inOther = r.agentOf(acme, "In A"), r.agentOf(acme, "In A 2"), r.agentOf(other, "In Other")
		var err error
		plain, err = r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Plain", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
	})
	// input is the full input that keeps a binding as it is, with another agent.
	input := func(b connectors.Binding, agentID string) studio.ConnectorInput {
		return studio.ConnectorInput{Name: b.Name, AgentID: agentID, Allow: b.Allow, Enabled: b.Enabled, Reply: b.Reply, Conversation: b.Conversation}
	}
	bound := func(a agents.Agent) connectors.Binding {
		return r.bind(studio.ConnectorInput{AgentID: a.ID, Allow: []string{"11"}})
	}
	It("keeps a project connection on the agents of its project", func() {
		b := bound(inA)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, input(b, inOther.ID))
		Expect(err).To(MatchError(connectors.ErrInvalid))
		Expect(err).To(MatchError(ContainSubstring(projectBinding)))
		_, err = r.conns.UpdateConnector(ctx, alice, b.ID, input(b, plain.ID))
		Expect(err).To(MatchError(connectors.ErrInvalid))
		Expect(err).To(MatchError(ContainSubstring(projectBinding)))
		got, err := r.conns.GetConnector(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AgentID).To(Equal(inA.ID))
	})
	It("keeps a connection that is not in a project off project agents", func() {
		b := bound(plain)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, input(b, inA.ID))
		Expect(err).To(MatchError(connectors.ErrInvalid))
		Expect(err).To(MatchError(ContainSubstring(plainBinding)))
	})
	It("lets a project connection move to another agent of the same project, and lists it there", func() {
		b := bound(inA)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, input(b, inA2.ID))
		Expect(err).NotTo(HaveOccurred())
		links, err := r.svc.ProjectConnections(ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(HaveLen(1))
		Expect(links[0].AgentID).To(Equal(inA2.ID))
		Expect(links[0].AgentName).To(Equal("In A 2"))
	})
	It("lets a binding start with any agent of the owner, and a project agent makes a project connection", func() {
		b := bound(inA)
		links, err := r.svc.ProjectConnections(ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(HaveLen(1))
		Expect(links[0].ID).To(Equal(b.ID))
	})
	It("still edits the other fields of a binding with the agent it has", func() {
		b := bound(inA)
		in := input(b, inA.ID)
		in.Name, in.Enabled = "Renamed", false
		got, err := r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Renamed"))
		on, err := r.conns.SetConnectorEnabled(ctx, alice, b.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(on.Enabled).To(BeTrue())
		p := bound(plain)
		in = input(p, plain.ID)
		in.Name = "Plain renamed"
		_, err = r.conns.UpdateConnector(ctx, alice, p.ID, in)
		Expect(err).NotTo(HaveOccurred())
	})
	It("denies a scope that was minted before the agent changed", func() {
		b := bound(inA)
		old := studio.BindingConnectorScope(b)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, input(b, inA2.ID))
		Expect(err).NotTo(HaveOccurred())
		_, err = r.conns.AuthorizeConnector(ctx, old, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: b.Kind})
		Expect(err).To(MatchError(connectors.ErrForbidden))
	})
	It("lists a binding whose agent was deleted, and a deleted project lists no connections", func() {
		b := bound(inA)
		Expect(r.svc.DeleteAgent(ctx, alice, inA.ID)).To(Succeed())
		list, err := r.conns.ListConnectors(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		links, err := r.svc.ProjectConnections(ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(BeEmpty())
		Expect(r.proj.Delete(ctx, alice, acme.ID)).To(Succeed())
		_, err = r.conns.GetConnector(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("a message to a project agent", func() {
		It("makes a chat of the project, and none while the project is archived", func() {
			r.bind(studio.ConnectorInput{AgentID: inA.ID, Allow: []string{"11"}})
			r.bot.say(1, 11, "Ada", "ada", "hi")
			waitFor("the answer", func() bool { return len(r.bot.answers()) == 1 })
			chats, err := r.svc.List(ctx, alice, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(chats).To(HaveLen(1))
			Expect(chats[0].ProjectID).To(Equal(acme.ID))
			n, err := r.svc.CountChatsInProject(ctx, alice, acme.ID, 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(1))

			_, err = r.proj.Archive(ctx, alice, acme.ID)
			Expect(err).NotTo(HaveOccurred())
			r.bot.say(2, 11, "Ada", "ada", "still there?")
			waitFor("the reply", func() bool { return len(r.bot.answers()) == 2 })
			Expect(r.bot.answers()[1]).To(Equal("This agent cannot work right now: its project is archived or was removed."))
			again, _ := r.svc.List(ctx, alice, 10)
			Expect(again).To(HaveLen(1), "no chat was made")

			_, err = r.proj.Restore(ctx, alice, acme.ID)
			Expect(err).NotTo(HaveOccurred())
			r.bot.say(3, 11, "Ada", "ada", "back")
			waitFor("the answer after restore", func() bool { return len(r.bot.answers()) == 3 })
			Expect(r.bot.answers()[2]).NotTo(ContainSubstring("cannot work"))
		})
		It("keeps the sender gates: an unpaired sender gets no chat", func() {
			r.untrusted = true
			r.bind(studio.ConnectorInput{AgentID: inA.ID, Allow: []string{"11"}})
			r.bot.say(1, 99, "Eve", "eve", "hi")
			Consistently(func() int {
				chats, _ := r.svc.List(ctx, alice, 10)
				return len(chats)
			}, "400ms", "50ms").Should(BeZero())
		})
	})
})
