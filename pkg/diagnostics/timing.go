// SPDX-License-Identifier: MIT
package diagnostics

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Kind string
type Phase string
type State string
type Outcome string

const (
	KindRequest      Kind    = "request"
	KindConfigReload Kind    = "config_reload"
	StateStart       State   = "start"
	StateEnd         State   = "end"
	StateMark        State   = "mark"
	OutcomeOK        Outcome = "ok"
	OutcomeError     Outcome = "error"
	OutcomeCanceled  Outcome = "canceled"
	OutcomeSkipped   Outcome = "skipped"

	PhaseExtraction          Phase = "extraction"
	PhaseBearerLookup        Phase = "bearer_lookup"
	PhaseDefaultListing      Phase = "default_listing"
	PhaseBodyLookup          Phase = "body_lookup"
	PhaseConfigLoadDefaults  Phase = "config_load_defaults"
	PhaseAliasResolution     Phase = "alias_resolution"
	PhaseConfigLockWait      Phase = "config_lock_wait"
	PhaseConfigLockHold      Phase = "config_lock_hold"
	PhaseConfigFilter        Phase = "config_filter"
	PhaseFSEnumeration       Phase = "fs_enumeration"
	PhaseLooseFilter         Phase = "loose_filter"
	PhaseExistenceFallback   Phase = "existence_fallback"
	PhaseModelInit           Phase = "model_init"
	PhaseModelRouterCallback Phase = "model_router_callback"
	PhaseReloadLockWait      Phase = "reload_lock_wait"
	PhaseReloadLockHold      Phase = "reload_lock_hold"
	PhaseReloadEnumeration   Phase = "reload_enumeration"
	PhaseReloadMetadata      Phase = "reload_metadata"
	PhaseReloadYAMLRead      Phase = "reload_yaml_read"
	PhaseReloadParseDefaults Phase = "reload_parse_defaults"
)

// Event deliberately has no free-form payload or error field.
type Event struct {
	ID      string
	Kind    Kind
	Phase   Phase
	State   State
	Outcome Outcome
	Elapsed time.Duration
	Count   int
}

// Sink must be safe for concurrent calls. Calls are synchronous; callers should
// use Record to emit aggregates after releasing application locks.
type Sink func(Event)

// Recorder is immutable after construction and may be shared between requests.
// A nil recorder disables timing.
type Recorder struct {
	sink Sink
	now  func() time.Time
}

// NewRecorder defaults to Info logging when sink is nil.
func NewRecorder(sink Sink) *Recorder {
	if sink == nil {
		sink = LogEvent
	}
	return &Recorder{sink: sink, now: time.Now}
}

type observationKey struct{}
type observation struct {
	recorder *Recorder
	id       string
	kind     Kind
}

func observed(ctx context.Context) *observation {
	if ctx == nil {
		return nil
	}
	o, _ := ctx.Value(observationKey{}).(*observation)
	return o
}

// HeaderDiagnosticID correlates an observed HTTP request with phase events.
const HeaderDiagnosticID = "X-LocalAI-Diagnostic-ID"

// RequestID returns the server-generated request UUID, or an empty string for
// nil, unobserved, or background-operation contexts. It never creates an ID.
func RequestID(ctx context.Context) string {
	if o := observed(ctx); o != nil && o.kind == KindRequest {
		return o.id
	}
	return ""
}

func (r *Recorder) Request(ctx context.Context) context.Context {
	if r == nil {
		return ctx
	}
	if o := observed(ctx); o != nil && o.kind == KindRequest {
		return ctx
	}
	return r.attach(ctx, KindRequest)
}

func (r *Recorder) Reload(ctx context.Context) context.Context {
	if r == nil {
		return ctx
	}
	return r.attach(ctx, KindConfigReload)
}

func (r *Recorder) attach(ctx context.Context, kind Kind) context.Context {
	return context.WithValue(ctx, observationKey{}, &observation{recorder: r, id: uuid.NewString(), kind: kind})
}

// Inherit transfers only diagnostic identity, leaving destination lifetime and
// other values intact. A source without observation leaves dst unchanged.
func Inherit(dst, src context.Context) context.Context {
	if o := observed(src); o != nil {
		return context.WithValue(dst, observationKey{}, o)
	}
	return dst
}

func Enabled(ctx context.Context) bool { return observed(ctx) != nil }

func noopEnd(Outcome, int) {}

// Begin emits a start immediately. The returned function should be called once,
// with the actual outcome; cancellation is not inferred or propagated here.
// Nested durations overlap and must not be summed as disjoint work.
func Begin(ctx context.Context, phase Phase) func(outcome Outcome, count int) {
	o := observed(ctx)
	if o == nil || !validPhase(phase) {
		return noopEnd
	}
	start := o.recorder.now()
	o.emit(phase, StateStart, OutcomeOK, 0, 0)
	return func(outcome Outcome, count int) { o.emit(phase, StateEnd, outcome, o.recorder.now().Sub(start), count) }
}

func Mark(ctx context.Context, phase Phase) {
	if o := observed(ctx); o != nil {
		o.emit(phase, StateMark, OutcomeOK, 0, 0)
	}
}

func Record(ctx context.Context, phase Phase, elapsed time.Duration, outcome Outcome, count int) {
	if o := observed(ctx); o != nil {
		o.emit(phase, StateEnd, outcome, elapsed, count)
	}
}

func (o *observation) emit(phase Phase, state State, outcome Outcome, elapsed time.Duration, count int) {
	e := Event{ID: o.id, Kind: o.kind, Phase: phase, State: state, Outcome: outcome, Elapsed: elapsed, Count: count}
	if validEvent(e) {
		o.recorder.sink(e)
	}
}

func validPhase(p Phase) bool {
	switch p {
	case PhaseExtraction, PhaseBearerLookup, PhaseDefaultListing, PhaseBodyLookup,
		PhaseConfigLoadDefaults, PhaseAliasResolution, PhaseConfigLockWait, PhaseConfigLockHold,
		PhaseConfigFilter, PhaseFSEnumeration, PhaseLooseFilter, PhaseExistenceFallback,
		PhaseModelInit, PhaseModelRouterCallback, PhaseReloadLockWait, PhaseReloadLockHold,
		PhaseReloadEnumeration, PhaseReloadMetadata, PhaseReloadYAMLRead, PhaseReloadParseDefaults:
		return true
	}
	return false
}

func validEvent(e Event) bool {
	if !validPhase(e.Phase) {
		return false
	}
	switch e.Kind {
	case KindRequest, KindConfigReload:
	default:
		return false
	}
	switch e.State {
	case StateStart, StateEnd, StateMark:
	default:
		return false
	}
	switch e.Outcome {
	case OutcomeOK, OutcomeError, OutcomeCanceled, OutcomeSkipped:
	default:
		return false
	}
	// LogEvent is exported: reject free-form identities even for direct calls.
	id, err := uuid.Parse(e.ID)
	return err == nil && id.Version() == 4 && id.Variant() == uuid.RFC4122 && id.String() == e.ID && e.Elapsed >= 0 && e.Count >= 0
}
