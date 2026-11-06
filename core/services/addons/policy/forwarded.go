// SPDX-License-Identifier: MIT
package policy

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Sharing is what a service of a kind that is not an add-on needs from the
// policy. *Policy is it in the process that holds the store (the add-on
// owner, combined mode included); Forwarded is it in every other process.
type Sharing interface {
	CreateRef(ctx context.Context, actor contract.Actor, kind string) (Ref, error)
	ListingKind(ctx context.Context, actor contract.Actor, kind string) ([]ShareView, error)
	// AdmitRef runs run while the actor may run the action. A lowering or a
	// revocation cancels the context given to run and waits for run.
	AdmitRef(ctx context.Context, actor contract.Actor, resource Ref, action Action, run func(context.Context) error) error
	GrantsRef(ctx context.Context, actor contract.Actor, ref Ref) (map[string]Role, error)
	RevokeRef(ctx context.Context, actor contract.Actor, resource Ref, userID string) error
	// EnsureRef and ForgetRef keep the record of a resource that its service
	// names itself; ListingKind gives that name in ShareView.Local.
	EnsureRef(ctx context.Context, actor contract.Actor, kind, local string) (Ref, error)
	ForgetRef(ctx context.Context, actor contract.Actor, ref Ref) error
	Authorize(ctx context.Context, actor contract.Actor, resource ResourceKey, action Action) error
}

var (
	_ Sharing = (*Policy)(nil)
	_ Sharing = Forwarded{}
)

// Forwarded is the policy of the add-on owner reached through the gateway of
// a frontend. Every call is decided by the owner. An owner that cannot be
// reached gives gateway.ErrUnavailable (or an unknown outcome that wraps it)
// and never a grant.
type Forwarded struct {
	Gateway *gateway.Gateway
}

func (f Forwarded) gw() (*gateway.Gateway, error) {
	if f.Gateway == nil {
		return nil, ErrUnavailable
	}
	return f.Gateway, nil
}

// CreateRef asks the owner for a new resource of the kind.
func (f Forwarded) CreateRef(ctx context.Context, actor contract.Actor, kind string) (Ref, error) {
	g, err := f.gw()
	if err != nil {
		return Ref{}, err
	}
	out, err := g.CreateShare(ctx, actor, kind)
	if err != nil {
		return Ref{}, err
	}
	if out.ResourceID == "" {
		return Ref{}, ErrUnavailable
	}
	return Ref{Kind: kind, ResourceID: out.ResourceID}, nil
}

// ListingKind is the owner's listing of the kind.
func (f Forwarded) ListingKind(ctx context.Context, actor contract.Actor, kind string) ([]ShareView, error) {
	g, err := f.gw()
	if err != nil {
		return nil, err
	}
	out, err := g.ShareViews(ctx, actor, kind)
	if err != nil {
		return nil, err
	}
	views := make([]ShareView, 0, len(out.Views))
	for _, v := range out.Views {
		views = append(views, ShareView{Ref: Ref{Kind: kind, ResourceID: v.ID}, Role: Role(v.Role), Everyone: v.Everyone, OwnerID: v.OwnerID, Shared: v.Shared, Local: v.Local})
	}
	return views, nil
}

// AdmitRef runs run here under an admission the owner holds.
func (f Forwarded) AdmitRef(ctx context.Context, actor contract.Actor, resource Ref, action Action, run func(context.Context) error) error {
	g, err := f.gw()
	if err != nil {
		return err
	}
	return g.RunShared(ctx, actor, resource.Kind, resource.ResourceID, string(action), run)
}

// GrantsRef is the owner's grant listing of a resource of the actor.
func (f Forwarded) GrantsRef(ctx context.Context, actor contract.Actor, ref Ref) (map[string]Role, error) {
	g, err := f.gw()
	if err != nil {
		return nil, err
	}
	list, err := g.ShareGrants(ctx, actor, ref.Kind, ref.ResourceID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Role, len(list.Grants))
	for _, gr := range list.Grants {
		out[gr.UserID] = Role(gr.Role)
	}
	return out, nil
}

// RevokeRef removes a grant through the owner, which answers once the work
// that grant alone allowed has stopped on every frontend.
func (f Forwarded) RevokeRef(ctx context.Context, actor contract.Actor, resource Ref, userID string) error {
	g, err := f.gw()
	if err != nil {
		return err
	}
	_, err = g.RevokeShare(ctx, actor, "", resource.Kind, resource.ResourceID, userID)
	return err
}

// EnsureRef asks the owner for the record of the actor's resource named
// local, made when there is none.
func (f Forwarded) EnsureRef(ctx context.Context, actor contract.Actor, kind, local string) (Ref, error) {
	g, err := f.gw()
	if err != nil {
		return Ref{}, err
	}
	out, err := g.EnsureShare(ctx, actor, kind, local)
	if err != nil {
		return Ref{}, err
	}
	if out.ResourceID == "" {
		return Ref{}, ErrUnavailable
	}
	return Ref{Kind: kind, ResourceID: out.ResourceID}, nil
}

// ForgetRef asks the owner to revoke every grant of the record and drop it.
func (f Forwarded) ForgetRef(ctx context.Context, actor contract.Actor, ref Ref) error {
	g, err := f.gw()
	if err != nil {
		return err
	}
	return g.ForgetShare(ctx, actor, ref.Kind, ref.ResourceID)
}

// Authorize answers a read of an add-on resource from the owner's listing of
// the resources of the add-on the actor may use. Any other action is denied
// here: nothing that is not a read is decided from a listing.
func (f Forwarded) Authorize(ctx context.Context, actor contract.Actor, resource ResourceKey, action Action) error {
	g, err := f.gw()
	if err != nil {
		return err
	}
	if action != ActionRead || resource.AddonID == "" || resource.ResourceID == "" {
		return ErrForbidden
	}
	list, err := g.ListResources(ctx, actor, resource.AddonID)
	if err != nil {
		return err
	}
	for _, r := range list.Resources {
		if r.ID == resource.ResourceID {
			return nil
		}
	}
	return ErrForbidden
}
