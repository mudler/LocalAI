// SPDX-License-Identifier: MIT

// Package policy is the owner-local authority for who may touch which addon
// resource. The addon process, the model and installation administration never
// decide access; this package does, from a durable store that is separate from
// the replaceable addon packages.
package policy

import (
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// ResourceKey names a resource by the trusted addon identity and an opaque
// host-issued resource ID.
type ResourceKey = gateway.ResourceKey

// Role is a grant on a whole resource. There are no per-table or per-row grants.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleOwner  Role = "owner"
)

func (r Role) rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleEditor:
		return 2
	case RoleOwner:
		return 3
	}
	return 0
}

// Action is what a caller wants to do with a resource.
type Action string

const (
	ActionRead   Action = "read"
	ActionOutput Action = "output"
	ActionWrite  Action = "write"
	ActionSchema Action = "schema"
	ActionShare  Action = "share"
	ActionDelete Action = "delete"
	// ActionResolve acknowledges an unresolved mutation outcome. It needs the
	// owner role and is never a typed dispatch or a proposal.
	ActionResolve Action = "resolve"
	// ActionUnknown is any action this package does not classify.
	ActionUnknown Action = "unknown"
)

// ParseAction classifies a gateway action name. Anything unrecognised is
// ActionUnknown, which is a mutation that needs the owner.
func ParseAction(name string) Action {
	switch Action(name) {
	case ActionRead, ActionOutput, ActionWrite, ActionSchema, ActionShare, ActionDelete, ActionResolve:
		return Action(name)
	}
	return ActionUnknown
}

// Mutation reports whether the action can change data, schema or sharing.
// Everything except an explicit read is one.
func (a Action) Mutation() bool { return a != ActionRead && a != ActionOutput }

// required is the least role that may run the action.
func (a Action) required() Role {
	switch a {
	case ActionRead, ActionOutput:
		return RoleViewer
	case ActionWrite:
		return RoleEditor
	}
	return RoleOwner
}

var (
	// ErrForbidden is a policy denial. It is the same error for a missing
	// resource and for a resource the caller may not touch.
	ErrForbidden = fmt.Errorf("addon policy denied: %w", gateway.ErrForbidden)
	// ErrUnavailable means the policy could not decide. It is never a grant.
	ErrUnavailable = fmt.Errorf("addon policy unavailable: %w", gateway.ErrUnavailable)
	// ErrDrainPending means a revocation is durable and effective for new work,
	// but admitted work did not stop within the bound. It is not acknowledged.
	ErrDrainPending = fmt.Errorf("addon revocation pending: %w", gateway.ErrUnavailable)
	// ErrQuota means the creation limits are reached. It is a denial of the
	// request, not an outage.
	ErrQuota = fmt.Errorf("addon resource quota reached: %w", gateway.ErrForbidden)
	// ErrMigrationRequired means the stored data belongs to another mode.
	ErrMigrationRequired = errors.New("addon policy store needs a stopped, backed-up ownership migration")
	// ErrDowngradeUnsupported means shared data cannot return to local-owner mode.
	ErrDowngradeUnsupported = errors.New("addon policy store holds shared data and cannot return to local-owner mode")
	// ErrStoreBusy means another process holds the store.
	ErrStoreBusy = errors.New("addon policy store is in use")
)
