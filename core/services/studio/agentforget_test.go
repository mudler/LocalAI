// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the share record of a deleted agent", func() {
	ctx := context.Background()

	owned := func(r *shareRig) []policy.ShareView {
		views, err := r.pol.ListingKind(ctx, ownerActor(alice), studio.AgentShareKind)
		Expect(err).NotTo(HaveOccurred())
		return views
	}

	It("is dropped with its grants when the owner deletes the agent", func() {
		r := newShareRig(sayOK)
		ref := r.share(agents.Agent{ID: "ag1", Name: "Gone"}, bob, policy.RoleUse)
		Expect(r.svc.DeleteAgent(ctx, alice, "ag1")).To(Succeed())
		_, ok := r.pol.OwnerOfRef(policy.Ref{Kind: studio.AgentShareKind, ResourceID: ref})
		Expect(ok).To(BeFalse())
		Expect(owned(r)).To(BeEmpty())
		list, _ := r.svc.AgentsWithShared(ctx, bob)
		Expect(list).To(BeEmpty())
	})

	It("is dropped when the owner deletes the agent and its notes", func() {
		r := newShareRig(sayOK)
		ref := r.share(agents.Agent{ID: "ag2", Name: "Gone too"}, bob, policy.RoleViewer)
		_, err := r.svc.DeleteAgentWithNotes(ctx, alice, "ag2", studio.AgentNotesKeep)
		Expect(err).NotTo(HaveOccurred())
		_, ok := r.pol.OwnerOfRef(policy.Ref{Kind: studio.AgentShareKind, ResourceID: ref})
		Expect(ok).To(BeFalse())
	})

	It("is dropped by the orphan sweep when its agent is gone from the store", func() {
		r := newShareRig(sayOK)
		ref := r.share(agents.Agent{ID: "ag3", Name: "Lost"}, bob, policy.RoleUse)
		// Deleted behind the service, as a version that did not drop the
		// record would have done.
		Expect(r.agents.Delete(ctx, alice, "ag3")).To(Succeed())
		rep, err := r.pol.SweepOrphans(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Dropped).To(Equal(1))
		_, ok := r.pol.OwnerOfRef(policy.Ref{Kind: studio.AgentShareKind, ResourceID: ref})
		Expect(ok).To(BeFalse())
	})

	It("is kept by the orphan sweep when the agent store is missing", func() {
		r := newShareRig(sayOK, func(d *studio.Deps) { d.Agents = nil })
		ref, err := r.pol.CreateRef(ctx, ownerActor(alice), studio.AgentShareKind)
		Expect(err).NotTo(HaveOccurred())
		r.grant(ref.ResourceID, bob, policy.RoleUse)
		rep, err := r.pol.SweepOrphans(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Dropped).To(BeZero())
		Expect(rep.Kept).To(Equal(1))
		_, ok := r.pol.OwnerOfRef(ref)
		Expect(ok).To(BeTrue(), "a missing store is not proof that the agent is gone")
		grants, err := r.pol.GrantsRef(ctx, ownerActor(alice), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(grants).To(HaveKeyWithValue(bob, policy.RoleUse))
	})

	It("never accumulates over 256 rounds of create, share and delete", func() {
		r := newShareRig(sayOK)
		for i := 0; i < policy.DefaultMaxSharesPerUser; i++ {
			id := fmt.Sprintf("loop%03d", i)
			r.share(agents.Agent{ID: id, Name: "Loop"}, bob, policy.RoleUse)
			Expect(r.svc.DeleteAgent(ctx, alice, id)).To(Succeed(), "round %d", i)
		}
		Expect(owned(r)).To(BeEmpty())
		// The owner can still share an agent: nothing holds the room.
		r.share(agents.Agent{ID: "after", Name: "After"}, bob, policy.RoleUse)
		Expect(owned(r)).To(HaveLen(1))
	})
})
