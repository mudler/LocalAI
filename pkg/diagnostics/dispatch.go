// SPDX-License-Identifier: MIT
package diagnostics

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	PhaseHTTPEntry             Phase = "http_entry"
	PhaseAuth                  Phase = "auth"
	PhaseBodyRead              Phase = "body_read"
	PhaseTraceStart            Phase = "trace_start"
	PhaseRoute                 Phase = "route"
	PhaseRouteRevision         Phase = "route_revision"
	PhaseReservation           Phase = "reservation"
	PhaseHealthProbe           Phase = "health_probe"
	PhaseHealthCache           Phase = "health_cache"
	PhaseLastUsed              Phase = "last_used"
	PhaseMetadataDecision      Phase = "metadata_decision"
	PhaseMetadataRPC           Phase = "metadata_rpc"
	PhaseMetadataEmpty         Phase = "metadata_empty"
	PhaseMetadataMarkerAbsent  Phase = "metadata_marker_absent"
	PhaseMetadataMarkerPresent Phase = "metadata_marker_present"
	PhaseMetadataNonempty      Phase = "metadata_nonempty"
	PhaseInflightIncrement     Phase = "inflight_increment"
	PhaseInflightDecrement     Phase = "inflight_decrement"
	PhaseReservationRelease    Phase = "reservation_release"
	PhaseClientLockWait        Phase = "client_lock_wait"
	PhaseDialAttempt           Phase = "dial_attempt"
	PhaseTransportReady        Phase = "transport_ready"
	PhaseRPC                   Phase = "rpc"
	PhaseRPCSent               Phase = "rpc_sent"
	PhaseRPCHealth             Phase = "rpc_health"
	PhaseRPCMetadata           Phase = "rpc_metadata"
	PhaseRPCPredict            Phase = "rpc_predict"
	PhaseRPCPredictStream      Phase = "rpc_predict_stream"
	PhaseRPCHeaders            Phase = "rpc_headers"
	PhaseRPCFirstMessage       Phase = "rpc_first_message"
	PhaseWorkerReceive         Phase = "worker_receive"
	PhaseWorkerPreparation     Phase = "worker_preparation"
	PhaseWorkerSubmit          Phase = "worker_submit"
	PhaseWorkerWrite           Phase = "worker_write"
	PhaseFrontendMessage       Phase = "frontend_message"
	PhaseFrontendCallback      Phase = "frontend_callback"
	PhaseSSERole               Phase = "sse_role"
	PhaseSSEContent            Phase = "sse_content"
	PhaseSSEReasoning          Phase = "sse_reasoning"
	PhaseSSEFlush              Phase = "sse_flush"
	PhaseDispatchSummary       Phase = "dispatch_summary"
)

func validDispatchPhase(p Phase) bool {
	switch p {
	case PhaseHTTPEntry, PhaseAuth, PhaseBodyRead, PhaseTraceStart, PhaseRoute,
		PhaseRouteRevision, PhaseReservation, PhaseHealthProbe, PhaseHealthCache,
		PhaseLastUsed, PhaseMetadataDecision, PhaseMetadataRPC, PhaseMetadataEmpty,
		PhaseMetadataNonempty, PhaseMetadataMarkerAbsent, PhaseMetadataMarkerPresent,
		PhaseInflightIncrement, PhaseInflightDecrement,
		PhaseReservationRelease, PhaseClientLockWait, PhaseDialAttempt,
		PhaseTransportReady, PhaseRPC, PhaseRPCSent, PhaseRPCHealth, PhaseRPCMetadata, PhaseRPCPredict, PhaseRPCPredictStream, PhaseRPCHeaders, PhaseRPCFirstMessage,
		PhaseWorkerReceive, PhaseWorkerPreparation, PhaseWorkerSubmit, PhaseWorkerWrite,
		PhaseFrontendMessage, PhaseFrontendCallback, PhaseSSERole, PhaseSSEContent,
		PhaseSSEReasoning, PhaseSSEFlush, PhaseDispatchSummary:
		return true
	}
	return false
}

// MaxDispatchEvents bounds lifetime collection, identities and milestone keys per request.
const MaxDispatchEvents = 256

// DispatchFields is optional so legacy events and their log schema remain unchanged.
// Numeric identities are request/process-local, never addresses or remote clock values.
type DispatchFields struct {
	Offset                            time.Duration
	Span, Parent, Attempt, Connection uint32
	Dropped, Emitted                  uint64
	SinkElapsed                       time.Duration
}

func validDispatchFields(e Event) bool {
	d := e.Dispatch
	if d == nil {
		return true
	}
	return e.Kind == KindRequest && d.Offset >= 0 && d.SinkElapsed >= 0 &&
		d.Span <= MaxDispatchEvents && d.Parent <= MaxDispatchEvents &&
		d.Attempt <= MaxDispatchEvents && d.Connection <= MaxDispatchEvents &&
		d.Emitted <= MaxDispatchEvents && (d.Parent == 0 || (d.Span != 0 && d.Parent < d.Span))
}

// AttachRequest accepts only canonical lowercase RFC4122 UUID v4 identities.
// Call only after independent worker opt-in, never to trust an HTTP client header.
// It preserves an existing request and returns false for a conflicting ID or nil recorder.
func (r *Recorder) AttachRequest(ctx context.Context, id string) (context.Context, bool) {
	if r == nil || ctx == nil {
		return ctx, false
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 || parsed.String() != id {
		return ctx, false
	}
	if o := observed(ctx); o != nil && o.kind == KindRequest {
		return ctx, o.id == id
	}
	return context.WithValue(ctx, observationKey{}, &observation{recorder: r, id: id, kind: KindRequest, origin: r.now()}), true
}

type milestoneKey struct {
	phase                     Phase
	span, attempt, connection uint32
}
type dispatchCollection struct {
	mu                                     sync.Mutex
	flushing                               bool
	flushPending                           bool
	events                                 []Event
	milestones                             map[milestoneKey]bool
	accepted, spans, attempts, connections uint32
	stats                                  DispatchStats
}

// DispatchScope is an immutable correlation handle; its zero value is a no-op.
// Copies may be used concurrently and retain the same request collection.
type DispatchScope struct {
	o                                 *observation
	c                                 *dispatchCollection
	span, parent, attempt, connection uint32
}

type dispatchScopeKey struct{}

// Context retains span/attempt parents without changing request identity or lifetime.
// Cross-request scopes and disabled scopes cannot attach an observation.
func (s DispatchScope) Context(ctx context.Context) context.Context {
	if s.o == nil || observed(ctx) != s.o {
		return ctx
	}
	return context.WithValue(ctx, dispatchScopeKey{}, s)
}

// Dispatch returns a request scope. Reload and disabled contexts remain inert.
// All enabled requests are observed; there is no request sampling control.
func Dispatch(ctx context.Context) DispatchScope {
	o := observed(ctx)
	if o == nil || o.kind != KindRequest {
		return DispatchScope{}
	}
	if s, ok := ctx.Value(dispatchScopeKey{}).(DispatchScope); ok && s.o == o {
		return s
	}
	o.dispatchMu.Lock()
	defer o.dispatchMu.Unlock()
	if o.dispatch == nil {
		o.dispatch = &dispatchCollection{milestones: make(map[milestoneKey]bool)}
	}
	return DispatchScope{o: o, c: o.dispatch}
}

func (s DispatchScope) identity(counter *uint32) uint32 {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if *counter == MaxDispatchEvents {
		s.c.drop()
		return 0
	}
	*counter++
	return *counter
}
func (c *dispatchCollection) drop() {
	if c.stats.Dropped < ^uint64(0) {
		c.stats.Dropped++
	}
}

// NewAttempt allocates a request-local attempt identity, preserving its parent scope.
// Exhaustion returns an inert scope and increments the drop counter.
func (s DispatchScope) NewAttempt() DispatchScope {
	if s.c == nil {
		return s
	}
	s.attempt = s.identity(&s.c.attempts)
	if s.attempt == 0 {
		return DispatchScope{}
	}
	return s
}

// NewConnection allocates a request-local connection identity. Reuse the returned
// scope for observations of that connection; this helper never creates a transport.
func (s DispatchScope) NewConnection() DispatchScope {
	if s.c == nil {
		return s
	}
	s.connection = s.identity(&s.c.connections)
	if s.connection == 0 {
		return DispatchScope{}
	}
	return s
}

// Start buffers the start boundary and returns a nested scope and exactly-once end.
// Boundaries are captured before collection, never by a sink. Outcomes are explicit.
func (s DispatchScope) Start(phase Phase) (DispatchScope, func(Outcome, int)) {
	if s.c == nil || !validDispatchPhase(phase) || phase == PhaseDispatchSummary {
		return DispatchScope{}, noopEnd
	}
	start := s.o.recorder.now()
	id := s.identity(&s.c.spans)
	if id == 0 {
		return DispatchScope{}, noopEnd
	}
	s.parent, s.span = s.span, id
	s.collect(phase, StateStart, OutcomeOK, start, 0, 0, false)
	var once sync.Once
	return s, func(outcome Outcome, count int) {
		once.Do(func() {
			now := s.o.recorder.now()
			s.collect(phase, StateEnd, outcome, now, now.Sub(start), count, false)
		})
	}
}

// MarkOnce records the first valid milestone per phase/span/attempt/connection.
func (s DispatchScope) MarkOnce(phase Phase, outcome Outcome) {
	if s.c != nil {
		s.collect(phase, StateMark, outcome, s.o.recorder.now(), 0, 0, true)
	}
}

// Record buffers an aggregate end boundary with an actual call count.
func (s DispatchScope) Record(phase Phase, elapsed time.Duration, outcome Outcome, count int) {
	if s.c != nil {
		s.collect(phase, StateEnd, outcome, s.o.recorder.now(), elapsed, count, false)
	}
}

func (s DispatchScope) collect(phase Phase, state State, outcome Outcome, now time.Time, elapsed time.Duration, count int, once bool) {
	if !validDispatchPhase(phase) || phase == PhaseDispatchSummary {
		return
	}
	e := Event{ID: s.o.id, Kind: KindRequest, Phase: phase, State: state, Outcome: outcome, Elapsed: elapsed, Count: count,
		Dispatch: &DispatchFields{Offset: now.Sub(s.o.origin), Span: s.span, Parent: s.parent, Attempt: s.attempt, Connection: s.connection}}
	if !validEvent(e) {
		return
	}
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	key := milestoneKey{phase, s.span, s.attempt, s.connection}
	if once && s.c.milestones[key] {
		return
	}
	if s.c.accepted == MaxDispatchEvents {
		s.c.drop()
		return
	}
	if once {
		s.c.milestones[key] = true
	}
	s.c.accepted++
	s.c.events = append(s.c.events, e)
}

// DispatchStats is cumulative. SinkElapsed sums synchronous event sink calls,
// excluding summary writes and collection overhead; concurrent durations may overlap.
type DispatchStats struct {
	Emitted, Dropped uint64
	SinkElapsed      time.Duration
}

// FlushDispatch synchronously drains buffered dispatch events outside collection
// locks, then emits a summary (outside the event budget). Call outside application
// locks and after producers finish for a complete report. It does not close the
// collection or replenish its lifetime budget. Concurrent/reentrant flushes return
// current stats and request that the active owner drain again before relinquishing
// ownership. Legacy Begin/Mark/Record remain synchronous.
func FlushDispatch(ctx context.Context) DispatchStats {
	o := observed(ctx)
	if o == nil {
		return DispatchStats{}
	}
	o.dispatchMu.Lock()
	c := o.dispatch
	o.dispatchMu.Unlock()
	if c == nil {
		return DispatchStats{}
	}
	c.mu.Lock()
	if c.flushing {
		c.flushPending = true
		stats := c.stats
		c.mu.Unlock()
		return stats
	}
	c.flushing = true
	for {
		c.flushPending = false
		events := c.events
		c.events = nil
		c.mu.Unlock()
		var elapsed time.Duration
		for _, e := range events {
			start := o.recorder.now()
			o.recorder.sink(e)
			elapsed += o.recorder.now().Sub(start)
		}
		c.mu.Lock()
		c.stats.Emitted += uint64(len(events))
		c.stats.SinkElapsed += elapsed
		stats := c.stats
		c.mu.Unlock()
		o.recorder.sink(Event{ID: o.id, Kind: KindRequest, Phase: PhaseDispatchSummary, State: StateMark, Outcome: OutcomeOK,
			Dispatch: &DispatchFields{Offset: o.recorder.now().Sub(o.origin), Dropped: stats.Dropped, Emitted: stats.Emitted, SinkElapsed: stats.SinkElapsed}})
		c.mu.Lock()
		// The current owner drains events requested by concurrent/reentrant completion
		// flushes. Checking and handing off ownership under mu avoids a lost wakeup.
		if c.flushPending && len(c.events) != 0 {
			continue
		}
		c.flushing = false
		c.mu.Unlock()
		return stats
	}
}
