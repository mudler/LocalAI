// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// agentRoles is the role table of the kind "agent": use runs the agent (the
// output of the agent is its run), viewer also reads the definition. Every
// other action (write, share, delete) is the owner's alone.
func agentRoles(a policy.Action) []policy.Role {
	switch a {
	case policy.ActionOutput:
		return []policy.Role{policy.RoleUse, policy.RoleViewer}
	case policy.ActionRead:
		return []policy.Role{policy.RoleViewer}
	}
	return nil
}

var _ policy.KindProvider = (*Service)(nil)

// RegisterKinds registers the share kind of saved agents with the sharing
// policy. Any active named account may receive a grant; a person needs the
// agents feature to use a shared agent, checked on every call.
func (s *Service) RegisterKinds(r policy.KindRegistrar) error {
	return r.RegisterKind(AgentShareKind, policy.KindSpec{
		Grantable: []policy.Role{policy.RoleUse, policy.RoleViewer},
		Roles:     agentRoles,
		Everyone:  true,
		Feature:   auth.FeatureAgents,
		Eligible: func(ctx context.Context, _ contract.Actor, userID string) (bool, error) {
			if s.d.Users == nil {
				return false, nil
			}
			u, err := s.d.Users(ctx, userID)
			if err != nil || u == nil {
				// The lookup answers for active accounts only; any failure
				// is a no, never a grant.
				return false, nil
			}
			return u.Status == "" || u.Status == auth.StatusActive, nil
		},
		// The service checks that the caller owns the agent before it asks
		// for a record (AgentShareRef).
		CreateAuthority: func(context.Context, contract.Actor) error { return nil },
		Backing: func(ctx context.Context, ref policy.Ref) (bool, error) {
			// Without a store nothing can say the agent is gone. An error
			// keeps the record, so the orphan sweep never drops a share
			// because this process has no agents.
			if s.d.Agents == nil {
				return false, errNoAgentStore
			}
			list, err := s.d.Agents.ListShared(ctx, []string{ref.ResourceID})
			return len(list) > 0, err
		},
	})
}

// PolicyAgentShares is AgentShares over the sharing policy of the add-on
// owner. Policy returns the policy of this process, nil when it holds none.
// Forward is that policy reached through the add-on carrier, for a process
// that holds none (a frontend of a cluster); nil, or a nil answer, means the
// owner cannot be reached from here. With neither, agents are not shared
// here, which fails closed.
type PolicyAgentShares struct {
	Policy  func() *policy.Policy
	Forward func() policy.Sharing
}

var _ AgentShares = PolicyAgentShares{}

func (p PolicyAgentShares) policy() policy.Sharing {
	if p.Policy != nil {
		if pol := p.Policy(); pol != nil {
			if pol.Mode() != policy.ModeNamedUser {
				return nil
			}
			if _, ok := pol.Shareable(AgentShareKind); !ok {
				return nil
			}
			return pol
		}
	}
	if p.Forward != nil {
		// The owner decides whether the kind is registered and sharing
		// runs; every call it refuses fails closed.
		if f := p.Forward(); f != nil {
			return f
		}
	}
	return nil
}

// actor is the identity of a call made for a person from the chats: the
// account is checked on every call, there is no session to check.
func agentActor(user string) contract.Actor {
	return contract.Actor{UserID: user, CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}
}

var errNoAgentPolicy = errors.New("studio: agent sharing is not available")

// errNoAgentStore is the answer of the backing check of an agent record in a
// process that has no agent store.
var errNoAgentStore = errors.New("studio: the agent store is not available")

func (p PolicyAgentShares) Available() bool { return p.policy() != nil }

func (p PolicyAgentShares) Create(ctx context.Context, user string) (string, error) {
	pol := p.policy()
	if pol == nil {
		return "", errNoAgentPolicy
	}
	ref, err := pol.CreateRef(ctx, agentActor(user), AgentShareKind)
	return ref.ResourceID, err
}

func viewOf(v policy.ShareView) AgentShareView {
	role := string(v.Role)
	if v.Role == policy.RoleViewer {
		role = AgentRoleView
	}
	return AgentShareView{Ref: v.Ref.ResourceID, Role: role, Everyone: v.Everyone, OwnerID: v.OwnerID, SharedWith: v.Shared}
}

func (p PolicyAgentShares) Listing(ctx context.Context, user string) ([]AgentShareView, error) {
	pol := p.policy()
	if pol == nil {
		return nil, errNoAgentPolicy
	}
	views, err := pol.ListingKind(ctx, agentActor(user), AgentShareKind)
	if err != nil {
		return nil, err
	}
	out := make([]AgentShareView, 0, len(views))
	for _, v := range views {
		out = append(out, viewOf(v))
	}
	return out, nil
}

func (p PolicyAgentShares) Role(ctx context.Context, user, ref string) (AgentShareView, error) {
	if ref == "" {
		return AgentShareView{}, agents.ErrNotFound
	}
	list, err := p.Listing(ctx, user)
	if err != nil {
		return AgentShareView{}, err
	}
	for _, v := range list {
		if v.Ref == ref {
			return v, nil
		}
	}
	return AgentShareView{}, agents.ErrNotFound
}

func (p PolicyAgentShares) Admit(ctx context.Context, user, ref string, run func(context.Context) error) error {
	pol := p.policy()
	if pol == nil {
		return errNoAgentPolicy
	}
	return pol.AdmitRef(ctx, agentActor(user), policy.Ref{Kind: AgentShareKind, ResourceID: ref}, policy.ActionOutput, run)
}

func (p PolicyAgentShares) RevokeAll(ctx context.Context, user, ref string) error {
	pol := p.policy()
	if pol == nil {
		return errNoAgentPolicy
	}
	r := policy.Ref{Kind: AgentShareKind, ResourceID: ref}
	// A listing through the owner is bounded, so the grants are read again
	// until none is left or a round removes nothing.
	for range maxRevokeRounds {
		grants, err := pol.GrantsRef(ctx, agentActor(user), r)
		if err != nil {
			return err
		}
		if len(grants) == 0 {
			return nil
		}
		var first error
		for principal := range grants {
			if err := pol.RevokeRef(ctx, agentActor(user), r, principal); err != nil && first == nil {
				first = err
			}
		}
		if first != nil {
			return first
		}
	}
	return nil
}

// maxRevokeRounds bounds the rounds of RevokeAll.
const maxRevokeRounds = 16

func (p PolicyAgentShares) AddonResourceReadable(ctx context.Context, user, addonID, resourceID string) bool {
	pol := p.policy()
	if pol == nil {
		return false
	}
	return pol.Authorize(ctx, agentActor(user), policy.ResourceKey{AddonID: addonID, ResourceID: resourceID}, policy.ActionRead) == nil
}
