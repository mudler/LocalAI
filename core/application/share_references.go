// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio"
)

// studioReferenceShares resolves a collection or a connection that a saved
// agent names to its record in the sharing API, for the agent's Share dialog.
// It makes the record the way the kind's own share route does
// (POST /api/collections/{name}/share, POST /api/chats/connectors/{id}/share):
// only for a thing the owner has, and without granting anything. The roles
// are the ones the kind registers.
func (a *Application) studioReferenceShares(ctx context.Context, user, kind, id string) (studio.ReferenceShareTarget, bool) {
	var (
		rid       string
		err       error
		grantable []policy.Role
	)
	switch kind {
	case KindCollection:
		rid, err = a.EnsureCollectionShare(ctx, user, id)
		grantable = collectionGrantable
	case KindConnection:
		svc := a.studioService
		if svc == nil || svc.Connectors() == nil {
			return studio.ReferenceShareTarget{}, false
		}
		rid, err = svc.Connectors().EnsureShare(ctx, user, id)
		grantable = connectionGrantable
	default:
		return studio.ReferenceShareTarget{}, false
	}
	if err != nil || rid == "" {
		return studio.ReferenceShareTarget{}, false
	}
	roles := make([]string, len(grantable))
	for i, r := range grantable {
		roles[i] = string(r)
	}
	return studio.ReferenceShareTarget{ResourceID: rid, Grantable: roles}, true
}
