// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/mudler/LocalAI/pkg/diagnostics"
	"google.golang.org/grpc/stats"
)

func TestDiagnosticFlushAdmission(t *testing.T) {
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("condition timed out")
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait(func() bool { return len(diagnosticFlushSlots) == 0 })
	before := DiagnosticFlushDrops()
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var entered atomic.Int32
	recorder := d.NewRecorder(func(d.Event) { entered.Add(1); <-release })
	// Distinct requests, not repeated flushes of a single bounded collection.
	for i := 0; i < maxDiagnosticFlushes; i++ {
		_, h := observeRPC(recorder.Request(context.Background()))
		h.finish()
	}
	wait(func() bool { return entered.Load() == maxDiagnosticFlushes })
	const producers = 128
	var wg sync.WaitGroup
	done := make(chan struct{})
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, h := observeRPC(recorder.Request(context.Background()))
			rpc := h.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: "/backend.Backend/Predict"})
			h.finish()
			var callbacks sync.WaitGroup
			for j := 0; j < 4; j++ {
				callbacks.Add(1)
				go func() {
					defer callbacks.Done()
					h.HandleRPC(rpc, &stats.InPayload{})
					h.HandleRPC(rpc, &stats.End{})
					h.HandleConn(ctx, &stats.ConnBegin{})
					h.finish()
				}()
			}
			callbacks.Wait()
		}()
	}
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("submission or stats callback blocked on sink")
	}
	if got := DiagnosticFlushDrops() - before; got != producers {
		t.Fatalf("drops=%d want %d (one per abandoned handler)", got, producers)
	}
	if got := len(diagnosticFlushSlots); got != maxDiagnosticFlushes {
		t.Fatalf("owners=%d", got)
	}
	if got := entered.Load(); got != maxDiagnosticFlushes {
		t.Fatalf("sink workers=%d", got)
	}
	once.Do(func() { close(release) })
	wait(func() bool { return len(diagnosticFlushSlots) == 0 })
	var recovered atomic.Bool
	_, h := observeRPC(d.NewRecorder(func(d.Event) { recovered.Store(true) }).Request(context.Background()))
	h.finish()
	wait(recovered.Load)
	wait(func() bool { return len(diagnosticFlushSlots) == 0 })
	if DiagnosticFlushDrops()-before != producers {
		t.Fatal("unexpected recovery drops")
	}
}

func TestDiagnosticFlushBoundsRedrains(t *testing.T) {
	before := DiagnosticFlushDrops()
	var h *rpcDiagnostics
	var summaries atomic.Int32
	ctx := d.NewRecorder(func(e d.Event) {
		if e.Phase == d.PhaseDispatchSummary {
			summaries.Add(1)
			h.lateFlush()
		}
	}).Request(context.Background())
	_, h = observeRPC(ctx)
	h.finish()
	deadline := time.Now().Add(5 * time.Second)
	for DiagnosticFlushDrops() == before || len(diagnosticFlushSlots) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("redrain owner did not terminate")
		}
		time.Sleep(time.Millisecond)
	}
	if summaries.Load() != d.MaxDispatchEvents+1 {
		t.Fatalf("passes=%d", summaries.Load())
	}
	if DiagnosticFlushDrops() != before+1 {
		t.Fatal("expected one abandonment")
	}
	h.finish()
	if summaries.Load() != d.MaxDispatchEvents+1 {
		t.Fatal("abandoned handler retried")
	}
}
