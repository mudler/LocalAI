// Package proposals keeps the change proposals of the chats: what a model
// asked to change in a person's own state, until the person answers. The
// record is contract.ChangeProposal, the same for every chat surface. A
// Store is shared by the frontends of a cluster (GORM) or private to one
// process (JSON file or memory).
package proposals

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var (
	// ErrNotFound: no such proposal, or it belongs to another person.
	ErrNotFound = errors.New("proposals: not found")
	// ErrTooManyPending: the person has the most proposals waiting.
	ErrTooManyPending = errors.New("proposals: too many proposals are waiting for an answer")
	// ErrRateLimited: the agent made the most proposals of the last hour.
	ErrRateLimited = errors.New("proposals: too many proposals in the last hour")
	// ErrExists: a proposal with that ID is stored already.
	ErrExists = errors.New("proposals: the ID is taken")
)

// Limits bound what Create accepts. A zero field sets no bound.
type Limits struct {
	// MaxPending is the most pending proposals of one owner.
	MaxPending int
	// PerWindow is the most proposals of one owner and agent created in the
	// Window before the new one. Agent "" is the plain chats of the owner.
	PerWindow int
	Window    time.Duration
}

// Event is one line of the audit. It never holds the content of a proposal,
// only the SHA-256 of it.
type Event struct {
	At       time.Time             `json:"at"`
	Owner    string                `json:"owner"`
	Action   string                `json:"action"`
	Proposal string                `json:"proposal,omitempty"`
	Kind     contract.ChangeKind   `json:"kind,omitempty"`
	Scope    contract.ChangeScope  `json:"scope,omitempty"`
	Target   string                `json:"target,omitempty"`
	Agent    string                `json:"agent,omitempty"`
	Source   contract.ChangeSource `json:"source,omitzero"`
	Rev      int64                 `json:"rev,omitempty"`
	Hash     string                `json:"hash,omitempty"`
	Detail   string                `json:"detail,omitempty"`
}

// The actions of the audit.
const (
	ActionPropose = "propose"
	ActionRefuse  = "refuse"
	ActionConfirm = "confirm"
	ActionDismiss = "dismiss"
	ActionExpire  = "expire"
	ActionStale   = "stale"
	ActionRevert  = "revert"
	// The actions of the reports that a trigger made from a connector chat
	// sends: a run delivered, a trigger paused because its capability is
	// off, and resumed.
	ActionDeliver = "deliver"
	ActionPause   = "pause"
	ActionResume  = "resume"
)

// Store keeps proposals and their audit. It is safe for concurrent use.
// proposalstest.Run checks every method.
type Store interface {
	// Create stores a pending proposal if the limits hold, atomically with
	// the counts. It returns ErrTooManyPending, ErrRateLimited or ErrExists.
	Create(ctx context.Context, p contract.ChangeProposal, lim Limits) error
	// Get returns the proposal of the owner, or ErrNotFound.
	Get(ctx context.Context, owner, id string) (contract.ChangeProposal, error)
	// List returns the proposals of the owner, newest first. With statuses it
	// keeps only those.
	List(ctx context.Context, owner string, statuses ...contract.ProposalStatus) ([]contract.ChangeProposal, error)
	// Settle moves a proposal from one status to another and sets Answered to
	// at, only while it has the status from, and reports whether it did. It is
	// the one write that decides an answer: two frontends that answer the same
	// proposal get true once. Moving to reverted sets RevertRev to rev.
	Settle(ctx context.Context, owner, id string, from, to contract.ProposalStatus, at time.Time, rev int64) (bool, error)
	// SetRev records the change set that applying wrote.
	SetRev(ctx context.Context, owner, id string, rev int64) error
	// Expire marks every pending proposal that expires at or before now and
	// returns them.
	Expire(ctx context.Context, now time.Time) ([]contract.ChangeProposal, error)
	// AddEvent appends a line to the audit.
	AddEvent(ctx context.Context, ev Event) error
	// Events returns the audit lines of the owner, newest first, at most limit.
	Events(ctx context.Context, owner string, limit int) ([]Event, error)
}
