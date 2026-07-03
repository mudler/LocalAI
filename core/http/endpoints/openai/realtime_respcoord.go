package openai

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/respcoord"
	"github.com/mudler/xlog"
)

// responseSink wires the explicit response-coordination state machine
// (respcoord.Coordinator — machine "M3" in docs/design/realtime-state-machines.md)
// into a realtime session.
//
// It replaces the legacy startResponse/cancelActiveResponse pair, whose
// activeResponse* fields were written from two goroutines (the client read-loop
// and the VAD goroutine) with the <-done wait performed outside the lock — the
// dual-writer race documented in Part 2 (failure mode 2). The coordinator
// serializes every start/cancel/finish decision behind one lock and guarantees
// at most one live response, so the two callers can no longer interleave into
// two overlapping responses.
//
// Each response runs as a goroutine spawned here. The effects map as:
//   - StartResponse:  spawn the registered body with a fresh cancelable context.
//   - CancelResponse: cancel that context (cooperative — the body stops at its
//     next ctx checkpoint and emits its own response.done{cancelled}).
//   - EmitTerminal:   currently a no-op. response.done is still emitted by the
//     response body itself; making this the single authoritative terminal (one
//     response.done per response.create, with Output+Usage populated) is the
//     next step and does not change the coordination guarantees here.
type responseSink struct {
	mu      sync.Mutex
	coord   *respcoord.Coordinator
	cancels map[respcoord.ResponseID]context.CancelFunc
	bodies  map[respcoord.ResponseID]responseBody
	seq     atomic.Uint64
	wg      sync.WaitGroup
}

type responseBody struct {
	parent context.Context
	run    func(ctx context.Context)
}

func newResponseSink() *responseSink {
	s := &responseSink{
		cancels: map[respcoord.ResponseID]context.CancelFunc{},
		bodies:  map[respcoord.ResponseID]responseBody{},
	}
	s.coord = respcoord.New(s)
	return s
}

// issue registers a response body and asks the coordinator to start it. Any
// in-flight response is superseded (cancelled, with its own terminal) first,
// atomically inside the coordinator — no caller-side locking, no dual-writer
// race. Non-blocking: the superseded response drains concurrently and its later
// Finished is ignored as stale.
func (s *responseSink) issue(parent context.Context, source respcoord.Source, run func(ctx context.Context)) {
	id := respcoord.ResponseID(s.seq.Add(1))
	s.mu.Lock()
	s.bodies[id] = responseBody{parent: parent, run: run}
	s.mu.Unlock()
	if err := s.coord.Apply(respcoord.Start{ID: id, Source: source}); err != nil {
		xlog.Error("respcoord: start failed", "error", err)
	}
}

// cancel cancels the in-flight response, if any. Non-blocking (barge-in must not
// stall the VAD tick).
func (s *responseSink) cancel(source respcoord.Source) {
	if err := s.coord.Apply(respcoord.Cancel{Source: source}); err != nil {
		xlog.Error("respcoord: cancel failed", "error", err)
	}
}

// wait blocks until every response goroutine (the active one plus any draining
// superseded ones) has exited. Used at teardown so the session is never deleted
// out from under a running response.
func (s *responseSink) wait() {
	s.wg.Wait()
}

// shutdown terminates the coordinator (cancelling any in-flight response) and
// then joins all response goroutines. After this the coordinator is in its
// absorbing Terminated state, so no further response can be issued — the
// connection (M1) parent's teardown uses this to guarantee no response outlives
// the session (see formal-verification/session_lifecycle.fizz).
func (s *responseSink) shutdown() {
	if err := s.coord.Apply(respcoord.Shutdown{}); err != nil {
		xlog.Error("respcoord: shutdown failed", "error", err)
	}
	s.wait()
}

// Perform executes one effect. It is called by Coordinator.Apply while the
// coordinator lock is held, so it must not block. It briefly takes s.mu but
// never acquires the coordinator lock while holding s.mu; the spawned
// goroutine's Finished apply takes the coordinator lock only AFTER releasing
// s.mu, so there is no lock cycle.
func (s *responseSink) Perform(e respcoord.Effect) {
	switch eff := e.(type) {
	case respcoord.StartResponse:
		s.mu.Lock()
		body := s.bodies[eff.ID]
		delete(s.bodies, eff.ID)
		parent := body.parent
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithCancel(parent)
		s.cancels[eff.ID] = cancel
		s.mu.Unlock()

		s.wg.Go(func() {
			defer func() {
				s.mu.Lock()
				delete(s.cancels, eff.ID)
				s.mu.Unlock()
				// Report completion. If this response was superseded/cancelled
				// the id is stale and the coordinator ignores it (so the
				// terminal is never emitted twice).
				if err := s.coord.Apply(respcoord.Finished{ID: eff.ID}); err != nil {
					xlog.Error("respcoord: finished apply failed", "error", err)
				}
			}()
			if body.run != nil {
				body.run(ctx)
			}
		})
	case respcoord.CancelResponse:
		s.mu.Lock()
		cancel := s.cancels[eff.ID]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case respcoord.EmitTerminal:
		// No-op for now: the response body still emits its own response.done.
		// Wiring the authoritative single terminal here is the next step.
	}
}
