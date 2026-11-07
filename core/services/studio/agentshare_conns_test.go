// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// trackerTransport serves one credential-only connector kind and answers
// nothing else: enough for bindings that only lend a credential.
type trackerTransport struct{}

func (trackerTransport) Kinds(context.Context) ([]studio.ConnectorKind, error) {
	return []studio.ConnectorKind{{AddonID: "tracker", ConnectorKind: contract.ConnectorKind{ID: "tracker", Title: "Tracker", Delivery: contract.DeliveryPull, AgentOptional: true}}}, nil
}

func (trackerTransport) Call(context.Context, studio.ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{}, nil
}

// The connections a shared agent names are the owner's bindings. A grantee's
// chat with the agent uses one only while the owner shares it with them, and
// never in place of the grantee's own choice.
var _ = Describe("the connections of a shared agent", func() {
	ctx := context.Background()
	var (
		r      *shareRig
		conns  *studio.Connectors
		shares *fakeShares
	)
	BeforeEach(func() {
		r = newShareRig(sayOK)
		shares = newFakeShares()
		var err error
		conns, err = studio.NewConnectors(r.svc, studio.ConnectorOptions{Store: connStore(), Transport: trackerTransport{}, Shares: shares})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		r.svc.SetConnectors(conns)
	})
	bind := func(user, name string) string {
		b, err := conns.CreateConnector(ctx, user, studio.ConnectorInput{Kind: "tracker", Name: name, Token: "token-" + name, Resources: []string{"acme/widgets"}})
		Expect(err).NotTo(HaveOccurred())
		return b.ID
	}
	needOf := func(user string) map[string]string {
		needs, err := r.svc.AgentNeeds(ctx, user, "ag1")
		Expect(err).NotTo(HaveOccurred())
		out := map[string]string{}
		for _, n := range needs {
			if n.Kind == "connection" {
				out[n.Label] = n.State
			}
		}
		return out
	}
	chatOf := func(user string) session.ChatRecord {
		rec, err := r.svc.Create(ctx, user, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		return rec
	}
	setOf := func(user string, rec session.ChatRecord) studio.ConnectionSet {
		rec, err := r.svc.Get(ctx, user, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		set, err := conns.ConnectionsOf(ctx, user, rec, "tracker")
		Expect(err).NotTo(HaveOccurred())
		return set
	}

	It("uses the one binding of the agent the grantee holds a grant on, says so in the needs, and stops when it is no longer shared", func() {
		work := bind(alice, "work")
		r.share(agents.Agent{ID: "ag1", Name: "Tracker", Connections: connsets.Sets{"tracker": {work}}}, bob, policy.RoleUse)
		Expect(needOf(bob)).To(Equal(map[string]string{"tracker connection 1": studio.ReferenceMissing}))
		rec := chatOf(bob)
		Expect(setOf(bob, rec).Items).To(BeEmpty(), "a binding that is not shared with the grantee is never used")

		shares.share(work, bob)
		Expect(needOf(bob)).To(Equal(map[string]string{"tracker connection 1": studio.ReferenceOK}))
		set := setOf(bob, rec)
		Expect(set.IDs()).To(Equal([]string{work}))
		Expect(set.By).To(Equal(studio.ConnectionByAgent))
		Expect(set.Items[0].Shared).To(BeTrue())

		shares.revoke(work, bob)
		Expect(setOf(bob, rec).Items).To(BeEmpty())
		Expect(needOf(bob)).To(Equal(map[string]string{"tracker connection 1": studio.ReferenceMissing}))
	})

	It("never puts a shared binding in place of the grantee's own, and lets the grantee choose it", func() {
		work := bind(alice, "work")
		r.share(agents.Agent{ID: "ag1", Name: "Tracker", Connections: connsets.Sets{"tracker": {work}}}, bob, policy.RoleUse)
		shares.share(work, bob)
		mine := bind(bob, "mine")
		rec := chatOf(bob)
		set := setOf(bob, rec)
		Expect(set.IDs()).To(Equal([]string{mine}), "the grantee's own binding")
		Expect(set.By).To(Equal(studio.ConnectionOnly))
		Expect(r.svc.SetConnections(ctx, bob, rec.ID, "tracker", []string{work})).To(Succeed(), "the agent's set does not lock the grantee's chat")
		set = setOf(bob, rec)
		Expect(set.IDs()).To(Equal([]string{work}))
		Expect(set.By).To(Equal(studio.ConnectionByChat))
	})

	It("picks none of several shared bindings by itself and offers them to the chat", func() {
		work, home := bind(alice, "work"), bind(alice, "home")
		r.share(agents.Agent{ID: "ag1", Name: "Tracker", Connections: connsets.Sets{"tracker": {work, home}}}, bob, policy.RoleUse)
		shares.share(work, bob)
		shares.share(home, bob)
		Expect(needOf(bob)).To(Equal(map[string]string{"tracker connection 1": studio.ReferenceOK, "tracker connection 2": studio.ReferenceOK}))
		rec := chatOf(bob)
		Expect(setOf(bob, rec).Items).To(BeEmpty())
		list, err := conns.Connections(ctx, bob, "tracker")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2), "both are offered to the chat")
		Expect(r.svc.SetConnections(ctx, bob, rec.ID, "tracker", []string{home})).To(Succeed())
		Expect(setOf(bob, rec).IDs()).To(Equal([]string{home}))
	})

	It("keeps the owner's own chats as they were: the agent's set, which the chat cannot change", func() {
		work, home := bind(alice, "work"), bind(alice, "home")
		r.share(agents.Agent{ID: "ag1", Name: "Tracker", Connections: connsets.Sets{"tracker": {work, home}}}, bob, policy.RoleUse)
		rec := chatOf(alice)
		set := setOf(alice, rec)
		Expect(set.IDs()).To(ConsistOf(work, home))
		Expect(set.By).To(Equal(studio.ConnectionByAgent))
		Expect(r.svc.SetConnections(ctx, alice, rec.ID, "tracker", []string{work})).To(MatchError(studio.ErrConnectionByAgent))
		Expect(needOf(alice)).To(Equal(map[string]string{"tracker connection 1": studio.ReferenceOK, "tracker connection 2": studio.ReferenceOK}))
	})
})
