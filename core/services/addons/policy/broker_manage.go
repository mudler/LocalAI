// SPDX-License-Identifier: MIT
package policy

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// InvalidateResource cancels every pending proposal on a resource, whoever
// made it, and makes a proposal that is being stored at this moment fail. The
// owner calls it when the resource is deleted. It returns once the change is
// durable; a failure leaves the deletion unacknowledged.
func (b *Broker) InvalidateResource(_ context.Context, resource ResourceKey) error {
	s := b.store
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidations[invalidationKey(resource.ResourceID, allUsers)]++
	return s.mutateLocked(func(d *proposalDocument) error {
		for _, r := range d.Proposals {
			if r.State == gateway.ProposalPending && r.ResourceID == resource.ResourceID && r.AddonID == resource.AddonID {
				r.State = gateway.ProposalCancelled
				r.Reason = "resource deleted"
				r.UpdatedAt = s.now()
			}
		}
		return nil
	})
}

// BlockedError is nil when no operation on the resource has an unresolved
// outcome. Otherwise it is the denial every write gets: it names the blocking
// operation to the resource's owner role only. A mutation of the UI-only
// management paths waits for it like any other write does.
func (b *Broker) BlockedError(actor contract.Actor, key ResourceKey) error {
	blocker := b.blockedBy(key.ResourceID, "")
	if blocker == "" {
		return nil
	}
	p, err := b.attached()
	if err != nil {
		return err
	}
	return b.blocked(p, actor, key, blocker)
}
