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
