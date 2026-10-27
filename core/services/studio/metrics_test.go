package studio_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

// metricsReader reads what the loop records. The global meter provider is set
// once for the test binary; the instruments of the loop bind to it whenever
// they are first used.
var metricsReader = func() *sdkmetric.ManualReader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

// turnCount is the value of localai_agent_runs_total for an outcome.
func turnCount(t *testing.T, outcome string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := metricsReader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "localai_agent_runs_total" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("localai_agent_runs_total is a %T, want a counter", m.Data)
			}
			for _, dp := range sum.DataPoints {
				if v, ok := dp.Attributes.Value("outcome"); ok && v.AsString() == outcome {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func TestATurnThatEndsIsCountedByOutcome(t *testing.T) {
	ctx := context.Background()

	t.Run("completed", func(t *testing.T) {
		before := turnCount(t, "completed")
		h := studiotest.NewGateHarness()
		svc := newService(t, h)
		id := create(t, svc, alice)
		sent(t, svc, alice, id, "hi")
		until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		h.Gate <- struct{}{} // let its one request through; the turn then finishes
		until(t, "the turn to end", func() bool { return turn(t, svc, alice, id).Last == store.TurnDone })
		until(t, "the turn to be counted", func() bool { return turnCount(t, "completed") == before+1 })
	})

	t.Run("cancelled", func(t *testing.T) {
		before := turnCount(t, "cancelled")
		h := studiotest.NewGateHarness()
		svc := newService(t, h)
		id := create(t, svc, alice)
		sent(t, svc, alice, id, "hi")
		until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		if err := svc.Stop(ctx, alice, id); err != nil {
			t.Fatal(err)
		}
		until(t, "the turn to stop", func() bool { return turn(t, svc, alice, id).Last == store.TurnStopped })
		until(t, "the turn to be counted", func() bool { return turnCount(t, "cancelled") == before+1 })
	})

	t.Run("error", func(t *testing.T) {
		before := turnCount(t, "error")
		h := studiotest.NewGateHarness()
		h.FailRequest = 1
		svc := newService(t, h)
		id := create(t, svc, alice)
		sent(t, svc, alice, id, "hi")
		until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		sent(t, svc, alice, id, "keep me")
		h.Gate <- struct{}{} // the request takes "keep me", then fails
		until(t, "the turn to fail", func() bool { return turn(t, svc, alice, id).Last == store.TurnFailed })
		until(t, "the turn to be counted", func() bool { return turnCount(t, "error") == before+1 })
	})
}
