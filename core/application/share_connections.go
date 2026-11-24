// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// connectionShares is the sharing policy of the connector bindings. It asks
// for the policy at each call: the add-on owner starts after the chats.
type connectionShares struct{ a *Application }

var _ studio.ConnectionShares = connectionShares{}

func (c connectionShares) Enabled() bool { return c.a.hostSharing() != nil }

func (c connectionShares) Shared(ctx context.Context, user string) ([]studio.SharedBinding, error) {
	refs, err := c.a.sharedRefs(ctx, KindConnection, user)
	if err != nil {
		return nil, err
	}
	out := make([]studio.SharedBinding, 0, len(refs))
	for _, r := range refs {
		out = append(out, studio.SharedBinding{BindingID: r.Name, OwnerName: r.OwnerName})
	}
	return out, nil
}

// Admit runs where the credential is used, the add-on owner, so its admission
// is the owner's own. It is the same call through the carrier.
func (c connectionShares) Admit(ctx context.Context, user, owner, bindingID string, run func(context.Context) error) error {
	p := c.a.hostSharing()
	if p == nil {
		return connectors.ErrNoCredential
	}
	ref, ok := c.a.sharedRefOf(ctx, KindConnection, user, owner, bindingID)
	if !ok {
		return connectors.ErrNoCredential
	}
	err := p.AdmitRef(ctx, hostActor(user), ref, policy.ActionRead, run)
	if denied(err) {
		// A revoked share is the answer of a binding that does not exist.
		return connectors.ErrNoCredential
	}
	return err
}

func (c connectionShares) ShareOf(ctx context.Context, owner, bindingID string) (studio.ShareState, bool) {
	v, ok := c.a.ownedRefs(ctx, KindConnection, owner)[bindingID]
	return studio.ShareState{ResourceID: v.Ref.ResourceID, SharedWith: v.Shared}, ok
}

func (c connectionShares) Ensure(ctx context.Context, owner, bindingID string) (string, error) {
	p := c.a.hostSharing()
	if p == nil {
		return "", studio.ErrSharingOff
	}
	ref, err := p.EnsureRef(ctx, hostActor(owner), KindConnection, bindingID)
	if err != nil {
		return "", err
	}
	return ref.ResourceID, nil
}

func (c connectionShares) Forget(ctx context.Context, owner, bindingID string) error {
	p := c.a.hostSharing()
	if p == nil {
		return nil
	}
	v, ok := c.a.ownedRefs(ctx, KindConnection, owner)[bindingID]
	if !ok {
		return nil
	}
	return p.ForgetRef(ctx, hostActor(owner), v.Ref)
}
