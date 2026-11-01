package studio_test

import (
	"context"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
func turnCount(outcome string) int64 {
	var rm metricdata.ResourceMetrics
	Expect(metricsReader.Collect(context.Background(), &rm)).To(Succeed())
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "localai_agent_runs_total" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			Expect(ok).To(BeTrue(), "localai_agent_runs_total is a %T, want a counter", m.Data)
			for _, dp := range sum.DataPoints {
				if v, ok := dp.Attributes.Value("outcome"); ok && v.AsString() == outcome {
					total += dp.Value
				}
			}
		}
	}
	return total
}

var _ = Describe("a turn that ends is counted by outcome", func() {
	ctx := context.Background()

	It("completed", func() {
		before := turnCount("completed")
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		h.Gate <- struct{}{} // let its one request through; the turn then finishes
		until("the turn to end", func() bool { return turn(svc, alice, id).Last == store.TurnDone })
		until("the turn to be counted", func() bool { return turnCount("completed") == before+1 })
	})

	It("cancelled", func() {
		before := turnCount("cancelled")
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		Expect(svc.Stop(ctx, alice, id)).To(Succeed())
		until("the turn to stop", func() bool { return turn(svc, alice, id).Last == store.TurnStopped })
		until("the turn to be counted", func() bool { return turnCount("cancelled") == before+1 })
	})

	It("error", func() {
		before := turnCount("error")
		h := studiotest.NewGateHarness()
		h.FailRequest = 1
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		sent(svc, alice, id, "keep me")
		h.Gate <- struct{}{} // the request takes "keep me", then fails
		until("the turn to fail", func() bool { return turn(svc, alice, id).Last == store.TurnFailed })
		until("the turn to be counted", func() bool { return turnCount("error") == before+1 })
	})
})
