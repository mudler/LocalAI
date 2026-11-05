// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// The results of a tool call, as the host log and the latest decisions of a
// connection name them.
const (
	OutcomeOK     = "ok"
	OutcomeDenied = "denied"
	OutcomeHeld   = "held"
	OutcomeError  = "error"
)

// Hold reasons of a write that waits for a person.
const (
	// HoldNeedsConfirmation: the write mode of the connection or the approval
	// of the tool asks for a person (see gateway.Proposal.AwaitsPerson).
	HoldNeedsConfirmation = "needs_confirmation"
	// HoldByHost: a rule of the host, such as a save level, asks for a person.
	HoldByHost = "held_by_host"
)

// Outcome records what became of one tool call, for the host log. The caller
// puts one in the context with WithOutcome; the adapter and the layers around
// it fill it as the call passes. It holds names, codes and numbers, never
// arguments, results, tokens or text. A call without one records nothing.
type Outcome struct {
	mu                                      sync.Mutex
	kind, binding, resource, result, reason string
	status                                  int
}

// OutcomeData is a copy of what an Outcome recorded.
type OutcomeData struct {
	// Kind is the kind of the tool: read, write, schema, create or action.
	Kind string
	// Binding is the connection the call used, or "".
	Binding string
	// Resource is the name of the resource the call worked on, or "".
	Resource string
	// Result is ok, denied, held or error; "" when nothing recorded it.
	Result string
	// Reason is the code of a result that is not ok.
	Reason string
	// Status is the status the service behind the add-on answered, or 0.
	Status int
}

type outcomeKey struct{}

// WithOutcome returns a context that carries a new Outcome, and the Outcome.
func WithOutcome(ctx context.Context) (context.Context, *Outcome) {
	o := &Outcome{}
	return context.WithValue(ctx, outcomeKey{}, o), o
}

// OutcomeFrom returns the Outcome of the context, or nil. The methods accept
// a nil receiver.
func OutcomeFrom(ctx context.Context) *Outcome {
	o, _ := ctx.Value(outcomeKey{}).(*Outcome)
	return o
}

// Call records which tool kind ran, and on what.
func (o *Outcome) Call(kind, resource string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if kind != "" {
		o.kind = kind
	}
	if resource != "" {
		o.resource = resource
	}
}

// Connection records the connection the call used.
func (o *Outcome) Connection(binding string) {
	if o == nil || binding == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.binding = binding
}

// Finish records how the call ended.
func (o *Outcome) Finish(result, reason string, status int) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.result, o.reason, o.status = result, reason, status
}

// Deny records a call that a rule of the host stopped before it reached the
// add-on.
func (o *Outcome) Deny(reason string) { o.Finish(OutcomeDenied, reason, 0) }

// Data returns a copy of what was recorded.
func (o *Outcome) Data() OutcomeData {
	if o == nil {
		return OutcomeData{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return OutcomeData{Kind: o.kind, Binding: o.binding, Resource: o.resource, Result: o.result, Reason: o.reason, Status: o.status}
}

// Classify says how a call ended: the result, the reason code and the status of
// the service behind the add-on, from what the adapter returned.
func Classify(res ToolResult, err error) (result, reason string, status int) {
	if err != nil {
		switch {
		case errors.Is(err, ErrUnknownTool):
			return OutcomeError, "unknown_tool", 0
		case errors.Is(err, ErrForbiddenArgument):
			return OutcomeError, "forbidden_argument", 0
		case errors.Is(err, ErrInvalidArguments):
			return OutcomeError, "invalid_arguments", 0
		case errors.Is(err, ErrResultTooLarge):
			return OutcomeError, "result_too_large", 0
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return OutcomeError, "canceled", 0
		case errors.Is(err, gateway.ErrStale):
			return OutcomeError, "stale", 0
		case errors.Is(err, gateway.ErrForbidden):
			return OutcomeDenied, "forbidden", 0
		case errors.Is(err, gateway.ErrUnavailable):
			return OutcomeError, "unavailable", 0
		}
		var unknown *gateway.OutcomeUnknown
		if errors.As(err, &unknown) {
			return OutcomeError, "outcome_unknown", 0
		}
		return OutcomeError, "error", 0
	}
	switch res.Kind {
	case ResultProposal:
		reason := HoldByHost
		if res.Proposal != nil && res.Proposal.Hold != "" {
			reason = res.Proposal.Hold
		}
		return OutcomeHeld, reason, 0
	case ResultError:
		switch res.Code {
		case CodeUpstreamRefused:
			return OutcomeError, "upstream_" + strconv.Itoa(res.Upstream), res.Upstream
		case CodeWriteNotAllowed:
			return OutcomeDenied, "write_mode_off", 0
		case CodeSelectResource, CodeNotPermitted, CodeResourceNotAllowed, CodeSelectConnection, CodeNoCredential,
			CodeResourceNotInAgentList, CodeResourceReadOnlyForAgent:
			return OutcomeDenied, res.Code, 0
		}
		return OutcomeError, res.Code, 0
	}
	return OutcomeOK, "", 0
}

type surfaceKey struct{}

// Surfaces a tool call can come from, for the text of a held change.
const (
	// SurfaceMessaging is a chat of a messaging app, where nobody can open the
	// approval card.
	SurfaceMessaging = "messaging"
)

// WithSurface returns a context that names where the person who gets the
// answer reads it. The chats service sets it; the default is the web UI.
func WithSurface(ctx context.Context, surface string) context.Context {
	return context.WithValue(ctx, surfaceKey{}, surface)
}

// SurfaceOf returns the surface of the context, or "".
func SurfaceOf(ctx context.Context) string {
	s, _ := ctx.Value(surfaceKey{}).(string)
	return s
}
