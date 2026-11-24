// SPDX-License-Identifier: MIT

// Package sharetest is an example of a resource kind that is shared through
// the policy. It is used only by tests: no production code imports it.
//
// A service shares its resources by implementing policy.KindProvider and
// handing it to the application before the add-on owner starts:
//
//	type Notes struct{ /* the service */ }
//
//	func (n *Notes) RegisterKinds(r policy.KindRegistrar) error {
//		return r.RegisterKind("note", policy.KindSpec{
//			Grantable: []policy.Role{policy.RoleUse, policy.RoleManage},
//			Roles:     roles, // the roles that may run each action
//			Everyone:  true,  // the owner may share a note with everyone
//			Feature:   "",    // no feature needed beyond a named account
//			Eligible:  n.mayReceive,
//			Backing:   n.exists,
//			CreateAuthority: n.mayCreate,
//		})
//	}
//
//	_ = app.RegisterShareKinds(notes)
//
// The service then creates its resources with Policy.CreateRef, runs its work
// under Policy.AdmitRef, and the owner shares them through /api/shares.
package sharetest

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Kind is the kind the example registers.
const Kind = "example:note"

// Roles is the role table of the example: use reads and runs a note, manage
// also changes it, and only the owner shares, deletes or does anything the
// table does not name.
func Roles(a policy.Action) []policy.Role {
	switch a {
	case policy.ActionRead, policy.ActionOutput:
		return []policy.Role{policy.RoleUse, policy.RoleManage}
	case policy.ActionWrite:
		return []policy.Role{policy.RoleManage}
	}
	return nil
}

// Provider registers the example kind.
type Provider struct {
	// Eligible decides who may receive a grant. Nil lets nobody receive one,
	// which is the default of every kind.
	Eligible func(ctx context.Context, actor contract.Actor, userID string) (bool, error)
	// NoEveryone turns the grant to everyone off.
	NoEveryone bool
	// Feature is the feature a caller needs, empty for none.
	Feature string
}

var _ policy.KindProvider = (*Provider)(nil)

// RegisterKinds registers Kind.
func (p *Provider) RegisterKinds(r policy.KindRegistrar) error {
	if p == nil {
		return errors.New("sharetest: nil provider")
	}
	return r.RegisterKind(Kind, policy.KindSpec{
		Grantable:       []policy.Role{policy.RoleUse, policy.RoleManage},
		Roles:           Roles,
		Everyone:        !p.NoEveryone,
		Eligible:        p.Eligible,
		Feature:         p.Feature,
		CreateAuthority: func(context.Context, contract.Actor) error { return nil },
		Backing:         func(context.Context, policy.Ref) (bool, error) { return true, nil },
	})
}
