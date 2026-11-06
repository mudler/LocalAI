// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"errors"
	"slices"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The host shares two kinds of its own resources through the policy: the
// connector bindings of the chats (kind connection) and the knowledge-base
// collections (kind collection). The records live in the policy store of the
// add-on owner, next to the add-on grants; the bindings and the collections
// stay where they are, and a share is a lookup through the record (see
// policy.EnsureRef), never a copy.

const (
	// KindConnection is the kind of a connector binding.
	KindConnection = "connection"
	// KindCollection is the kind of a collection.
	KindCollection = "collection"
)

// The roles each kind grants. The registration and the choices the Share
// dialog of an agent offers for a reference of the kind both read them.
var (
	connectionGrantable = []policy.Role{policy.RoleUse}
	collectionGrantable = []policy.Role{policy.RoleViewer, policy.RoleEditor}
)

// connectionRoles: use reads and runs the binding's add-on tools with its
// credential. Every other action is the owner's.
func connectionRoles(a policy.Action) []policy.Role {
	switch a {
	case policy.ActionRead, policy.ActionOutput:
		return []policy.Role{policy.RoleUse}
	}
	return nil
}

// collectionRoles: viewer searches and reads the entries, editor also adds
// and removes entries. Sources, reset and sharing are the owner's.
func collectionRoles(a policy.Action) []policy.Role {
	switch a {
	case policy.ActionRead, policy.ActionOutput:
		return []policy.Role{policy.RoleViewer, policy.RoleEditor}
	case policy.ActionWrite:
		return []policy.Role{policy.RoleEditor}
	}
	return nil
}

// hostShareKinds registers the two kinds. It is a policy.KindProvider.
type hostShareKinds struct{ a *Application }

var _ policy.KindProvider = hostShareKinds{}

func (k hostShareKinds) RegisterKinds(r policy.KindRegistrar) error {
	// Records are made active by EnsureRef after the service checked that the
	// caller owns the binding or the collection, so nothing is ever pending
	// and the existence check has nothing to settle.
	exists := func(context.Context, policy.Ref) (bool, error) { return true, nil }
	create := func(context.Context, contract.Actor) error { return nil }
	err := r.RegisterKind(KindConnection, policy.KindSpec{
		Grantable:       slices.Clone(connectionGrantable),
		Roles:           connectionRoles,
		Everyone:        false,
		Feature:         auth.FeatureConnectors,
		Eligible:        k.holds(auth.FeatureConnectors),
		CreateAuthority: create,
		Backing:         exists,
	})
	if err != nil {
		return err
	}
	return r.RegisterKind(KindCollection, policy.KindSpec{
		Grantable:       slices.Clone(collectionGrantable),
		Roles:           collectionRoles,
		Everyone:        true,
		Feature:         auth.FeatureCollections,
		Eligible:        k.holds(auth.FeatureCollections),
		CreateAuthority: create,
		Backing:         exists,
	})
}

// holds is the rule of who may receive a grant of a kind: an active named
// account that holds the feature the kind needs. The policy asks it only
// about active named accounts.
func (k hostShareKinds) holds(feature string) func(context.Context, contract.Actor, string) (bool, error) {
	return func(ctx context.Context, _ contract.Actor, userID string) (bool, error) {
		if k.a.authDB == nil {
			return false, nil
		}
		err := auth.AccountRevalidate(k.a.authDB, auth.AddonModeNamedUser)(ctx, hostActor(userID), feature)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, auth.ErrAddonUnavailable):
			return false, err
		}
		return false, nil
	}
}

// registerHostShareKinds hands the two kinds to the policy once.
func (a *Application) registerHostShareKinds() error {
	var err error
	a.hostKindsOnce.Do(func() { err = a.RegisterShareKinds(hostShareKinds{a}) })
	return err
}

// hostActor is the actor of the host's own work for a person: the chats
// credential, which the account check accepts for an active account and
// which no request carries.
func hostActor(userID string) contract.Actor {
	return contract.Actor{UserID: userID, CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}
}

// hostSharing returns the sharing policy of the host's resources when this
// process can share them: named accounts and the policy of the add-on owner,
// held here (the owner, combined mode included) or reached through the add-on
// carrier (a frontend, see addonSharingForward). The owner decides every call
// of a frontend, and an owner that cannot be reached shares nothing.
func (a *Application) hostSharing() policy.Sharing {
	if a.authDB == nil {
		return nil
	}
	if p := a.AddonPolicy(); p != nil {
		if p.Mode() != policy.ModeNamedUser {
			return nil
		}
		return p
	}
	if f := a.addonSharingForward(); f != nil {
		return f
	}
	return nil
}

// ownerNames returns the display names of the owners of shared resources. A
// lookup that fails leaves the names empty: a listing without them is right.
func (a *Application) ownerNames(ctx context.Context, ids []string) map[string]string {
	if a.authDB == nil || len(ids) == 0 {
		return map[string]string{}
	}
	names, err := auth.NewAddonUsers(a.authDB).Lookup(ctx, ids)
	if err != nil {
		return map[string]string{}
	}
	return names
}

// sharedRef is a resource of a kind shared with a person, with the service's
// name of it and the display name of its owner.
type sharedRef struct {
	Ref                    policy.Ref
	Owner, OwnerName, Name string
	Role                   policy.Role
	Everyone               bool
}

// sharedRefs lists the resources of the kind shared with user. The listing
// carries the owner and the name of each record, so it reads the same on the
// owner and through the carrier.
func (a *Application) sharedRefs(ctx context.Context, kind, user string) ([]sharedRef, error) {
	p := a.hostSharing()
	if p == nil {
		return nil, nil
	}
	views, err := p.ListingKind(ctx, hostActor(user), kind)
	if err != nil {
		return nil, err
	}
	var out []sharedRef
	var owners []string
	for _, v := range views {
		if v.Role == policy.RoleOwner || v.Local == "" || v.OwnerID == "" {
			continue
		}
		out = append(out, sharedRef{Ref: v.Ref, Owner: v.OwnerID, Name: v.Local, Role: v.Role, Everyone: v.Everyone})
		owners = append(owners, v.OwnerID)
	}
	names := a.ownerNames(ctx, owners)
	for i := range out {
		out[i].OwnerName = names[out[i].Owner]
	}
	return out, nil
}

// ownedRefs lists the owner's records of the kind by the service's name of
// each, with the number of grants. A name that was never made shareable has
// no entry; a listing that fails gives none.
func (a *Application) ownedRefs(ctx context.Context, kind, owner string) map[string]policy.ShareView {
	out := map[string]policy.ShareView{}
	p := a.hostSharing()
	if p == nil || owner == "" {
		return out
	}
	views, err := p.ListingKind(ctx, hostActor(owner), kind)
	if err != nil {
		return out
	}
	for _, v := range views {
		if v.Role == policy.RoleOwner && v.OwnerID == owner && v.Local != "" {
			out[v.Local] = v
		}
	}
	return out
}

// sharedRefOf finds the record that user holds a grant on for the resource
// that owner names local. ok is false when user holds none now.
func (a *Application) sharedRefOf(ctx context.Context, kind, user, owner, local string) (policy.Ref, bool) {
	refs, err := a.sharedRefs(ctx, kind, user)
	if err != nil {
		return policy.Ref{}, false
	}
	for _, r := range refs {
		if r.Owner == owner && r.Name == local {
			return r.Ref, true
		}
	}
	return policy.Ref{}, false
}

// denied says whether err is a refusal of the policy, here or through the
// carrier, rather than an owner that could not decide.
func denied(err error) bool {
	return errors.Is(err, gateway.ErrForbidden) && !errors.Is(err, gateway.ErrUnavailable)
}
