package proposals

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrNotGranted means the chat may not change this kind of state.
var ErrNotGranted = errors.New("proposals: this agent was not given the right to make this change")

// What a chat asks the gate for.
const (
	GatePropose = "propose"
	GateRevert  = "revert"
)

// Request is one question to the gate. Agent is the saved agent of the chat,
// empty for a plain chat. Source says which surface the chat came from, by
// the host's own names, never a platform's.
type Request struct {
	Action string
	User   string
	Agent  string
	Kind   contract.ChangeKind
	Scope  contract.ChangeScope
	Source contract.ChangeSource
}

// Gate decides whether a model may propose or revert in a chat. The grants of
// a saved agent plug in here; until they exist the default applies.
type Gate interface {
	Allow(ctx context.Context, r Request) error
}

// GateFunc makes a function a Gate.
type GateFunc func(ctx context.Context, r Request) error

// Allow calls f.
func (f GateFunc) Allow(ctx context.Context, r Request) error { return f(ctx, r) }

// DefaultGate allows a plain chat and refuses a chat that runs a saved agent:
// an agent has no right until the owner grants it.
var DefaultGate Gate = GateFunc(func(_ context.Context, r Request) error {
	if r.Agent != "" {
		return ErrNotGranted
	}
	return nil
})
