// SPDX-License-Identifier: MIT
package studio

import (
	"cmp"
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/executionrequest"
	"github.com/mudler/LocalAI/core/services/studio/session"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
)

// ExecutionObserver admits the owner-selected provider, not a discovery claim.
// The request and explanation reader belong exclusively to this outer scope.
type ExecutionObserver interface {
	// The last result reads what the provider reported beside the typed value,
	// or why there is none. It is never nil for a nil request that failed.
	NewExecutionObservation(context.Context, string, string) (*executionrequest.Request, string, func() ExecutionDetail)
}

// UnknownReason says why the host has no execution status for a chat. The
// codes reach the API and the chat page, which words them.
type UnknownReason string

const (
	// UnknownOwnerUnreachable: the process that owns the add-on did not answer.
	UnknownOwnerUnreachable UnknownReason = "owner_unreachable"
	// UnknownNotReported: the add-on declares no execution status to read.
	UnknownNotReported UnknownReason = "not_reported"
	// UnknownStale: the add-on changed (version or runtime) while it was read.
	UnknownStale UnknownReason = "stale"
	// UnknownInvalid: the add-on answered with a status the host refuses.
	UnknownInvalid UnknownReason = "invalid_status"
	// UnknownNotSelected: the add-on does not run this chat's commands.
	UnknownNotSelected UnknownReason = "not_selected"
	// UnknownNoAnswer: the add-on did not answer in time.
	UnknownNoAnswer UnknownReason = "no_answer"
	// UnknownReportedUnknown: the add-on answered that it cannot tell.
	UnknownReportedUnknown UnknownReason = "reported_unknown"
	// UnknownDraft: the chat does not exist yet, so no add-on is bound to it.
	UnknownDraft UnknownReason = "draft"
)

// ExecutionDetail is what the provider said beside the typed value, plus why
// no status could be read. Failure is meaningful only while the value is
// unknown.
type ExecutionDetail struct {
	Explanation string
	// NetworkPolicyEnforced is the provider's report; the host trusts it only
	// for an available, isolated value.
	NetworkPolicyEnforced bool
	Failure               UnknownReason
}

type executionObservationKey struct{}
type executionObservation struct {
	ctx                context.Context
	user, conversation string
	// mu guards the fields below. A scope that is closed never starts again.
	mu        sync.Mutex
	closed    bool
	started   bool
	attempted time.Time
	// retriable is set when the last observation failed in a way a fresh one
	// may fix (the owner restarted or did not answer).
	retriable bool
	request   *executionrequest.Request
	addon     string
	detail    func() ExecutionDetail
	provider  ExecutionObserver
}

// executionScope borrows existing outer state. Nested consumers must not close
// it: only the real turn, priming operation or standalone API request owns Close.
func (s *Service) executionScope(ctx context.Context, user, conversation string) (context.Context, func()) {
	if existing, ok := ctx.Value(executionObservationKey{}).(*executionObservation); ok && existing.user == user && existing.conversation == conversation {
		return ctx, func() {}
	}
	provider, _ := s.d.AddonTools.(ExecutionObserver)
	ownerCtx, cancel := context.WithCancel(ctx)
	scope := &executionObservation{ctx: ownerCtx, user: user, conversation: conversation, provider: provider}
	return context.WithValue(ctx, executionObservationKey{}, scope), func() {
		cancel()
		scope.mu.Lock()
		defer scope.mu.Unlock()
		scope.closed = true
		if scope.request != nil {
			scope.request.Close()
		}
	}
}
func (s *Service) productScope(ctx context.Context, x session.Access) (context.Context, func()) {
	a := x.Actor()
	return s.executionScope(ctx, a.Principal, a.ChatID)
}

// ExecutionObservation never falls back to an arbitrary installed provider.
// An out-of-scope, wrong-user or non-selected add-on has unknown status.
func ExecutionObservation(ctx context.Context, user, addon string) (model.Value, string) {
	value, detail := ObserveExecution(ctx, user, addon)
	return value, detail.Explanation
}

var scopelessOnce sync.Once

// onScopeless is called when a status is read from a context that has no
// execution scope. That is a programming error: the read cannot succeed and
// the caller would report "not selected" for an add-on that is fine. It logs
// once; the tests replace it to fail.
var onScopeless = func() {
	scopelessOnce.Do(func() {
		slog.Error("studio: the execution status was read outside an execution scope; the status is reported as not selected", "stack", string(debug.Stack()))
	})
}

// ObserveExecution is ExecutionObservation with the whole detail. Failure says
// why the value is unknown; Explanation and NetworkPolicyEnforced are set only
// when the provider answered.
func ObserveExecution(ctx context.Context, user, addon string) (model.Value, ExecutionDetail) {
	scope, ok := ctx.Value(executionObservationKey{}).(*executionObservation)
	if !ok {
		onScopeless()
		return model.Unknown(), ExecutionDetail{Failure: UnknownNotSelected}
	}
	if user != scope.user {
		return model.Unknown(), ExecutionDetail{Failure: UnknownNotSelected}
	}
	if scope.conversation == "" {
		return model.Unknown(), ExecutionDetail{Failure: UnknownDraft}
	}
	scope.mu.Lock()
	if !scope.closed && scope.provider != nil && (!scope.started || (scope.retriable && time.Since(scope.attempted) >= observationRetryAfter)) {
		// The first read of a scope stands. A turn can outlive a restart of the
		// add-on owner, so after a failure a restart may fix, one fresh
		// observation (a new binding and a new read) is allowed per interval.
		if scope.request != nil {
			scope.request.Close()
		}
		scope.request, scope.addon, scope.detail = scope.provider.NewExecutionObservation(scope.ctx, scope.user, scope.conversation)
		scope.started, scope.attempted, scope.retriable = true, time.Now(), false
	}
	request, scopeAddon, detailOf := scope.request, scope.addon, scope.detail
	scope.mu.Unlock()
	read := func() ExecutionDetail {
		if detailOf == nil {
			return ExecutionDetail{}
		}
		return detailOf()
	}
	settle := func(v model.Value, d ExecutionDetail) (model.Value, ExecutionDetail) {
		scope.mu.Lock()
		scope.retriable = v.Readiness == model.UnknownReadiness && d.Failure.retriable()
		scope.mu.Unlock()
		return v, d
	}
	if request == nil {
		d := read()
		return settle(model.Unknown(), ExecutionDetail{Failure: cmp.Or(d.Failure, UnknownNotReported)})
	}
	if addon != scopeAddon {
		return model.Unknown(), ExecutionDetail{Failure: UnknownNotSelected}
	}
	value := request.Observe(ctx)
	d := read()
	if value.Readiness == model.UnknownReadiness {
		failure := cmp.Or(d.Failure, UnknownReportedUnknown)
		out := ExecutionDetail{Failure: failure}
		if failure == UnknownReportedUnknown {
			out.Explanation = d.Explanation
		}
		return settle(value, out)
	}
	d.Failure = ""
	return value, d
}

// observationRetryAfter is the least time between two observations of one
// scope. A test may lower it.
var observationRetryAfter = 5 * time.Second

// retriable says whether a fresh observation may succeed where this failure
// did. A refusal of the binding or of the payload will not change by itself.
func (r UnknownReason) retriable() bool {
	return r == UnknownOwnerUnreachable || r == UnknownNoAnswer || r == UnknownStale
}

// Text is the reason in a sentence for a person or a model, without the
// subject: "its status could not be read: " + Text().
func (r UnknownReason) Text() string {
	switch r {
	case UnknownOwnerUnreachable:
		return "the add-on owner could not be reached"
	case UnknownNotReported:
		return "the add-on has not reported its execution status"
	case UnknownStale:
		return "the status did not match the add-on's current version"
	case UnknownInvalid:
		return "the add-on sent a status the host could not accept"
	case UnknownNotSelected:
		return "the add-on is not the one that runs this chat's commands"
	case UnknownNoAnswer:
		return "the add-on did not answer in time"
	case UnknownReportedUnknown:
		return "the add-on reported that it cannot tell"
	case UnknownDraft:
		return "the chat has not been created yet"
	}
	return "the reason is not known"
}

// ExecutionStatusView is observation data, never execution authorization.
type ExecutionStatusView struct {
	Readiness   string `json:"readiness"`
	Isolation   string `json:"isolation"`
	Explanation string `json:"explanation,omitempty"`
	// UnknownReason is set while the status is unknown: why the host could not
	// read one.
	UnknownReason string `json:"unknown_reason,omitempty"`
	// NetworkPolicyEnforced is true only when the provider reported that its
	// commands reach the network through the host's policy and the status is
	// available and isolated. The host uses it; the page does not.
	NetworkPolicyEnforced bool   `json:"-"`
	Generation            string `json:"generation,omitempty"`
	Incarnation           string `json:"incarnation,omitempty"`
}

func executionStatusView(ctx context.Context, user, addon string) *ExecutionStatusView {
	v, d := model.Unknown(), ExecutionDetail{Failure: UnknownNotSelected}
	if scope, ok := ctx.Value(executionObservationKey{}).(*executionObservation); ok && scope.user == user && scope.conversation == "" {
		addon, d.Failure = "", UnknownDraft // a draft has no add-on bound yet
	}
	if addon != "" {
		v, d = ObserveExecution(ctx, user, addon)
	}
	out := &ExecutionStatusView{Readiness: string(v.Readiness), Isolation: string(v.Isolation), Explanation: d.Explanation, UnknownReason: string(d.Failure),
		NetworkPolicyEnforced: d.NetworkPolicyEnforced && v.Readiness == model.Available && v.Isolation == model.Isolated}
	scope, ok := ctx.Value(executionObservationKey{}).(*executionObservation)
	if ok && scope.user == user {
		scope.mu.Lock()
		if scope.addon == addon && scope.request != nil {
			b := scope.request.Binding()
			out.Generation, out.Incarnation = b.Generation, b.Incarnation
		}
		scope.mu.Unlock()
	}
	return out
}
