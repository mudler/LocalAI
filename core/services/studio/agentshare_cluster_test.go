// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/gateway/carriertest"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/resources"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// clusterUsers is the account directory of the owner's resource service.
type clusterUsers struct{}

func (clusterUsers) Lookup(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if n, ok := shareUsers[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

func (clusterUsers) Eligible(_ context.Context, id string) (bool, error) {
	_, ok := shareUsers[id]
	return ok, nil
}

func (clusterUsers) Search(context.Context, string, int) ([]gateway.UserRef, error) { return nil, nil }
func (clusterUsers) Accounts(context.Context, string, int) ([]gateway.UserRef, error) {
	return nil, nil
}

func (clusterUsers) Active(_ context.Context, id string) error {
	if _, ok := shareUsers[id]; !ok {
		return gateway.ErrForbidden
	}
	return nil
}

// clusterRig is the add-on owner, which holds the sharing policy, and two
// frontends with their own chat services that reach it through the real
// carrier. The saved agents are in one store, as in a cluster's database.
type clusterRig struct {
	net        *carriertest.Fixture
	pol        *policy.Policy
	frontA     *gateway.Gateway
	svcA, svcB *studio.Service
	agents     agents.Store
}

func newClusterRig(script func(workspace.Actor, string) []studiotest.ToolStep) *clusterRig {
	r := &clusterRig{}
	as, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.agents = as
	users := func(_ context.Context, id string) (*auth.User, error) {
		name, ok := shareUsers[id]
		if !ok {
			return nil, errors.New("no such user")
		}
		return &auth.User{ID: id, Name: name, Status: auth.StatusActive}, nil
	}
	st, err := policy.OpenStore(GinkgoT().TempDir(), policy.ModeNamedUser)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = st.Close() })
	r.pol = policy.New(st, policy.Options{
		DrainTimeout: 5 * time.Second,
		Revalidate:   func(context.Context, contract.Actor) error { return nil },
		Account: func(_ context.Context, a contract.Actor, _ string) error {
			if _, ok := shareUsers[a.UserID]; !ok {
				return policy.ErrForbidden
			}
			return nil
		},
	})
	// The owner runs a chat service too, which registers the kind "agent"
	// when the policy opens.
	owner, _ := newToolService(sayOK, func(d *studio.Deps) { d.Agents, d.Users = as, users })
	Expect(policy.RegisterKinds(r.pol, owner)).To(Succeed())
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	host := policy.NewShareHost(ctx, r.pol)
	host.Wait = 300 * time.Millisecond
	allow := func(context.Context, contract.Actor) error { return nil }
	manage := resources.New(resources.Options{Policy: r.pol, Users: clusterUsers{}})
	r.net, err = carriertest.New(func() *gateway.Gateway {
		return &gateway.Gateway{Revalidate: allow, RevalidateAccount: allow, Sharing: manage, ShareAuthority: host}
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(r.net.Close)
	r.frontA = r.net.Frontend(carriertest.FrontA)
	frontB := r.net.Frontend(carriertest.FrontB)
	frontend := func(g *gateway.Gateway) *studio.Service {
		svc, _ := newToolService(script, func(d *studio.Deps) {
			d.Files, d.Agents, d.Users = newFiles(), as, users
			// A frontend holds no policy: every call goes to the owner.
			d.AgentShares = studio.PolicyAgentShares{
				Policy:  func() *policy.Policy { return nil },
				Forward: func() policy.Sharing { return policy.Forwarded{Gateway: g} },
			}
		})
		return svc
	}
	r.svcA, r.svcB = frontend(r.frontA), frontend(frontB)
	return r
}

func (r *clusterRig) session(user string) contract.Actor {
	return contract.Actor{UserID: user, CredentialID: "session:" + user, Mode: auth.AddonModeNamedUser}
}

var _ = Describe("shared saved agents on two frontends", func() {
	ctx := context.Background()

	It("shares on one frontend, lists and runs on the other, and a revoke on the first stops the run on the second", func() {
		release := make(chan struct{})
		defer close(release)
		r := newClusterRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "long" {
				return []studiotest.ToolStep{{Block: release}, {Say: "late"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		Expect(r.svcA.AgentSharingAvailable()).To(BeTrue())
		Expect(r.agents.Put(ctx, agents.Agent{ID: "ag1", Name: "Worker", Principal: alice, Created: time.Now().UTC()})).To(Succeed())
		ref, err := r.svcA.AgentShareRef(ctx, alice, "ag1")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.frontA.SetShare(ctx, r.session(alice), "", studio.AgentShareKind, ref, bob, string(policy.RoleViewer))
		Expect(err).NotTo(HaveOccurred())

		list, err := r.svcB.AgentsWithShared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Agent.ID).To(Equal("ag1"))
		Expect(list[0].Access.Role).To(Equal(studio.AgentRoleView))
		Expect(list[0].Access.OwnerName).To(Equal("Alice"))
		own, err := r.svcA.AgentsWithShared(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(own).To(HaveLen(1))
		Expect(own[0].Access.SharedWith).To(Equal(1))

		rec, err := r.svcB.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		sent(r.svcB, bob, rec.ID, "long")
		until("the turn to run on the second frontend", func() bool { return turn(r.svcB, bob, rec.ID).Running })

		_, err = r.frontA.RevokeShare(ctx, r.session(alice), "", studio.AgentShareKind, ref, bob)
		Expect(err).NotTo(HaveOccurred())
		// The revoke answers only once the turn on the other frontend stopped.
		Expect(turn(r.svcB, bob, rec.ID).Running).To(BeFalse())
		Expect(turn(r.svcB, bob, rec.ID).Last).To(Equal(store.TurnStopped))
		_, err = r.svcB.Send(ctx, bob, rec.ID, "again")
		Expect(err).To(MatchError(studio.ErrAgentNotShared))
		list, err = r.svcB.AgentsWithShared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("shows only the own agents and runs no shared turn while the owner is gone", func() {
		r := newClusterRig(sayOK)
		Expect(r.agents.Put(ctx, agents.Agent{ID: "ag1", Name: "Worker", Principal: alice, Created: time.Now().UTC()})).To(Succeed())
		ref, err := r.svcA.AgentShareRef(ctx, alice, "ag1")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.frontA.SetShare(ctx, r.session(alice), "", studio.AgentShareKind, ref, bob, string(policy.RoleUse))
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svcB.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		r.net.Close()
		list, err := r.svcB.AgentsWithShared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		_, err = r.svcB.Send(ctx, bob, rec.ID, "hi")
		Expect(err).To(HaveOccurred())
		Expect(turn(r.svcB, bob, rec.ID).Running).To(BeFalse())
	})
})
