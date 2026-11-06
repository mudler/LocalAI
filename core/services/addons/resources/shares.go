// SPDX-License-Identifier: MIT
package resources

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The generic sharing API serves the resource kinds that are not add-ons:
// a service registers its kind with the policy (policy.KindProvider) and
// gets listing, grants, revocation and the user picker from here, with the
// same rules as add-on sharing. Add-on kinds are refused like an unknown
// kind: they are shared through the add-on routes, which also check the
// add-on account rule.

var _ gateway.ShareManager = (*Service)(nil)

// rules returns the sharing rules of a kind the API serves. Any other kind is
// the plain denial, the answer of a resource the caller may not see.
func (s *Service) rules(kind string) (policy.ShareRules, error) {
	if err := s.sharing(); err != nil {
		return policy.ShareRules{}, err
	}
	if !policy.ValidKind(kind) {
		return policy.ShareRules{}, gateway.ErrForbidden
	}
	r, ok := s.o.Policy.Shareable(kind)
	if !ok {
		return policy.ShareRules{}, gateway.ErrForbidden
	}
	return r, nil
}

// shareRef checks the kind and the owner role and returns the resource.
func (s *Service) shareRef(ctx context.Context, actor contract.Actor, kind, resourceID string) (policy.Ref, policy.ShareRules, error) {
	r, err := s.rules(kind)
	if err != nil {
		return policy.Ref{}, r, err
	}
	if resourceID == "" || len(resourceID) > 256 {
		return policy.Ref{}, r, gateway.ErrForbidden
	}
	ref := policy.Ref{Kind: kind, ResourceID: resourceID}
	if err = s.ownedRef(ctx, actor, ref); err != nil {
		return policy.Ref{}, r, err
	}
	return ref, r, nil
}

func roleNames(roles []policy.Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = string(r)
	}
	return out
}

func (s *Service) shareList(ctx context.Context, actor contract.Actor, ref policy.Ref, r policy.ShareRules) (gateway.ShareList, error) {
	grants, err := s.grants(ctx, actor, ref)
	if err != nil {
		return gateway.ShareList{}, err
	}
	return gateway.ShareList{Kind: ref.Kind, ResourceID: ref.ResourceID, Roles: roleNames(r.Grantable), Everyone: r.Everyone, Grants: grants}, nil
}

// Shares lists the resources of the kind the actor owns and those shared with
// the actor, each with the actor's role and, for a shared one, the owner's
// display name. Resources of other people that are not shared with the actor
// are never part of the answer.
func (s *Service) Shares(ctx context.Context, actor contract.Actor, kind string) (gateway.ShareIndex, error) {
	r, err := s.rules(kind)
	if err != nil {
		return gateway.ShareIndex{}, err
	}
	views, err := s.o.Policy.ListingKind(ctx, actor, kind)
	if err != nil {
		return gateway.ShareIndex{}, classify(err)
	}
	out := gateway.ShareIndex{Kind: kind, Roles: roleNames(r.Grantable), Everyone: r.Everyone, Owned: []gateway.SharedResource{}, Shared: []gateway.SharedResource{}}
	var owners []string
	seen := map[string]bool{}
	var shared []policy.ShareView
	for _, v := range views {
		if v.Role == policy.RoleOwner {
			if len(out.Owned) >= MaxListed {
				out.Truncated = true
				continue
			}
			n := v.Shared
			out.Owned = append(out.Owned, gateway.SharedResource{ID: v.Ref.ResourceID, Role: string(v.Role), SharedWith: &n})
			continue
		}
		if len(shared) >= MaxListed {
			out.Truncated = true
			continue
		}
		shared = append(shared, v)
		if !seen[v.OwnerID] {
			seen[v.OwnerID] = true
			owners = append(owners, v.OwnerID)
		}
	}
	var names map[string]string
	if s.o.Users != nil && len(owners) > 0 {
		// The names are a convenience: a listing without them is still right.
		names, _ = s.o.Users.Lookup(ctx, owners)
	}
	for _, v := range shared {
		out.Shared = append(out.Shared, gateway.SharedResource{ID: v.Ref.ResourceID, Role: string(v.Role), Everyone: v.Everyone, Owner: cleanName(names[v.OwnerID])})
	}
	return out, nil
}

// ShareGrants lists the grants of a resource of the kind for its owner.
func (s *Service) ShareGrants(ctx context.Context, actor contract.Actor, kind, resourceID string) (gateway.ShareList, error) {
	ref, r, err := s.shareRef(ctx, actor, kind, resourceID)
	if err != nil {
		return gateway.ShareList{}, err
	}
	return s.shareList(ctx, actor, ref, r)
}

// SetShare gives a user, or every account with policy.Everyone, a role the
// kind grants. The kind's own rule decides who may receive it. Lowering a
// role stops the admitted work it took away before it returns.
func (s *Service) SetShare(ctx context.Context, actor contract.Actor, kind, resourceID, principal, role string) (gateway.ShareList, error) {
	ref, r, err := s.shareRef(ctx, actor, kind, resourceID)
	if err != nil {
		return gateway.ShareList{}, err
	}
	grantable := false
	for _, g := range r.Grantable {
		grantable = grantable || string(g) == role
	}
	if !grantable {
		return gateway.ShareList{}, gateway.ErrInvalidRequest
	}
	if principal == "" || len(principal) > 256 || (principal == policy.Everyone && !r.Everyone) {
		return gateway.ShareList{}, gateway.ErrForbidden
	}
	// A user grant goes to an active named account only; the kind's rule
	// then decides among them. The owner role is checked above, so nobody
	// else learns which accounts exist.
	if principal != policy.Everyone {
		if s.o.Users == nil {
			return gateway.ShareList{}, gateway.ErrUnavailable
		}
		if err = s.o.Users.Active(ctx, principal); err != nil {
			return gateway.ShareList{}, classify(err)
		}
	}
	if err = s.o.Policy.SetGrantRef(ctx, actor, ref, principal, policy.Role(role)); err != nil {
		return gateway.ShareList{}, classify(err)
	}
	return s.shareList(ctx, actor, ref, r)
}

// RevokeShare removes the grant of a user, or the grant to every account.
// It returns once the work that grant alone allowed has stopped.
func (s *Service) RevokeShare(ctx context.Context, actor contract.Actor, kind, resourceID, principal string) (gateway.ShareList, error) {
	ref, r, err := s.shareRef(ctx, actor, kind, resourceID)
	if err != nil {
		return gateway.ShareList{}, err
	}
	if err = s.revoke(ctx, actor, ref, principal); err != nil {
		return gateway.ShareList{}, err
	}
	return s.shareList(ctx, actor, ref, r)
}

// SearchShareUsers is the user picker of the owner of a resource of the kind.
// It leaves out the people the kind's rule would refuse.
func (s *Service) SearchShareUsers(ctx context.Context, actor contract.Actor, kind, resourceID, query string, limit int) (gateway.UserList, error) {
	ref, _, err := s.shareRef(ctx, actor, kind, resourceID)
	if err != nil {
		return gateway.UserList{}, err
	}
	// It searches every active account, so a query needs three characters
	// and each person has a bounded rate of searches.
	owner, _ := s.o.Policy.OwnerOfRef(ref)
	return s.accounts.Search(ctx, actor.UserID, owner, query, limit, func(id string) (bool, error) {
		return s.o.Policy.EligibleRef(ctx, actor, ref, id)
	})
}
