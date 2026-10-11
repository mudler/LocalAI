// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	d "github.com/mudler/LocalAI/pkg/diagnostics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

// At most 16 asynchronous flush owners process-wide; there is no waiting queue.
const maxDiagnosticFlushes = 16

var diagnosticFlushSlots = make(chan struct{}, maxDiagnosticFlushes)
var diagnosticFlushDrops atomic.Uint64

// DiagnosticFlushDrops returns the process-local cumulative number of gRPC
// handlers whose asynchronous drain was abandoned on admission saturation or
// after exhausting its bounded redrain passes.
// It does not call a sink and remains available while sinks are stalled.
// Counts handlers, not events or unique requests; resets on process restart.
func DiagnosticFlushDrops() uint64 { return diagnosticFlushDrops.Load() }

const diagnosticMetadataKey = "localai-diagnostic-id"

// A handler belongs to one per-call ClientConn, not to the shared Client.
// Connection contexts do not inherit the RPC context; retain correlation here.
type rpcDiagnostics struct {
	ctx                                    context.Context
	scope                                  d.DispatchScope
	mu                                     sync.Mutex
	released, flushing, pending, abandoned bool
}
type rpcObservation struct {
	scope     d.DispatchScope
	end       func(d.Outcome, int)
	methodEnd func(d.Outcome, int)
}
type rpcObservationKey struct{}

func observeRPC(ctx context.Context) (context.Context, *rpcDiagnostics) {
	id := d.RequestID(ctx)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 || parsed.String() != id {
		return ctx, nil
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(diagnosticMetadataKey, id)
	ctx = metadata.NewOutgoingContext(ctx, md)
	scope := d.Dispatch(ctx).NewConnection()
	scope.MarkOnce(d.PhaseDialAttempt, d.OutcomeSkipped)
	return ctx, &rpcDiagnostics{ctx: ctx, scope: scope}
}
func (h *rpcDiagnostics) options() []grpc.DialOption {
	if h == nil {
		return nil
	}
	return []grpc.DialOption{grpc.WithStatsHandler(h)}
}
func (h *rpcDiagnostics) finish() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.released = true
	h.mu.Unlock()
	h.lateFlush()
}

// Coalesce asynchronous drains without waiting for sink capacity. Admission
// precedes goroutine creation. Rejected handlers never retry, even on late
// callbacks; no queue or goroutine retains their context on behalf of the drain.
// An admitted owner can live as long as the synchronous sink blocks (possibly
// forever), but there can only be maxDiagnosticFlushes such owners process-wide.
func (h *rpcDiagnostics) lateFlush() {
	h.mu.Lock()
	if !h.released || h.abandoned {
		h.mu.Unlock()
		return
	}
	h.pending = true
	if h.flushing {
		h.mu.Unlock()
		return
	}
	select {
	case diagnosticFlushSlots <- struct{}{}:
	default:
		h.abandoned = true
		diagnosticFlushDrops.Add(1)
		h.mu.Unlock()
		return
	}
	h.flushing = true
	h.mu.Unlock()
	go func() {
		defer func() { <-diagnosticFlushSlots }()
		// Bound redrains too, even if a callback source continues after completion.
		for pass := 0; pass <= d.MaxDispatchEvents; pass++ {
			h.mu.Lock()
			h.pending = false
			h.mu.Unlock()
			d.FlushDispatch(h.ctx)
			h.mu.Lock()
			if h.pending {
				if pass < d.MaxDispatchEvents {
					h.mu.Unlock()
					continue
				}
				h.abandoned = true
				diagnosticFlushDrops.Add(1)
			}
			h.flushing = false
			h.mu.Unlock()
			return
		}
	}()
}
func (h *rpcDiagnostics) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}
func (h *rpcDiagnostics) HandleConn(_ context.Context, s stats.ConnStats) {
	if _, ok := s.(*stats.ConnBegin); ok {
		h.scope.MarkOnce(d.PhaseTransportReady, d.OutcomeOK)
	}
	h.lateFlush()
}
func (h *rpcDiagnostics) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	var phase d.Phase
	switch info.FullMethodName {
	case "/backend.Backend/Health":
		phase = d.PhaseRPCHealth
	case "/backend.Backend/ModelMetadata":
		phase = d.PhaseRPCMetadata
	case "/backend.Backend/Predict":
		phase = d.PhaseRPCPredict
	case "/backend.Backend/PredictStream":
		phase = d.PhaseRPCPredictStream
	default:
		return ctx
	}
	scope, end := h.scope.Start(d.PhaseRPC)
	_, methodEnd := scope.Start(phase)
	return context.WithValue(ctx, rpcObservationKey{}, &rpcObservation{scope, end, methodEnd})
}
func rpcOutcome(err error) d.Outcome {
	if err == nil {
		return d.OutcomeOK
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.Canceled || status.Code(err) == codes.DeadlineExceeded {
		return d.OutcomeCanceled
	}
	return d.OutcomeError
}
func (h *rpcDiagnostics) HandleRPC(ctx context.Context, s stats.RPCStats) {
	observation, _ := ctx.Value(rpcObservationKey{}).(*rpcObservation)
	if observation == nil {
		return
	}
	switch event := s.(type) {
	case *stats.OutPayload:
		observation.scope.MarkOnce(d.PhaseRPCSent, d.OutcomeOK)
	case *stats.InHeader:
		observation.scope.MarkOnce(d.PhaseRPCHeaders, d.OutcomeOK)
	case *stats.InPayload:
		observation.scope.MarkOnce(d.PhaseRPCFirstMessage, d.OutcomeOK)
	case *stats.End:
		outcome := rpcOutcome(event.Error)
		observation.methodEnd(outcome, 1)
		observation.end(outcome, 1)
	}
	h.lateFlush()
}

func (c *Client) diagnosticLock(ctx context.Context) func() {
	if c.parallel {
		return func() {}
	}
	_, end := d.Dispatch(ctx).Start(d.PhaseClientLockWait)
	c.opMutex.Lock()
	end(d.OutcomeOK, 1)
	return c.opMutex.Unlock
}
