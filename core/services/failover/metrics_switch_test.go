package failover

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var (
	switchReaderOnce sync.Once
	switchReader     *sdkmetric.ManualReader
)

// switchCount reads localai_failover_switches_total. The global provider can
// only be installed once per process, so every spec shares one reader and
// compares counts before and after.
func switchCount() int64 {
	switchReaderOnce.Do(func() {
		switchReader = sdkmetric.NewManualReader()
		otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(switchReader)))
	})
	var rm metricdata.ResourceMetrics
	Expect(switchReader.Collect(context.Background(), &rm)).To(Succeed())
	var n int64
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			if mt.Name != "localai_failover_switches_total" {
				continue
			}
			if sum, ok := mt.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					n += dp.Value
				}
			}
		}
	}
	return n
}

var _ = Describe("switch metric", func() {
	It("counts a switch once across the cluster, on the frontend that decided it", func() {
		clock := newFakeClock()
		src := newFakeSource(remote("x"), local("y"), chainCfg("chain", nil, t("x"), t("y")))
		bus := &loopSync{pins: map[string]string{}}
		leaderIsA := true
		gateFor := func(isA bool) LeaderGate {
			return func(_ context.Context, fn func()) bool {
				if isA != leaderIsA {
					return false
				}
				fn()
				return true
			}
		}
		a := New(src, WithClock(clock), WithLeaderGate(gateFor(true)))
		b := New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		bus.add(a)
		bus.add(b)
		a.SetStateSync(bus)
		b.SetStateSync(bus)
		a.Tick(context.Background())
		b.Tick(context.Background())

		before := switchCount()
		a.ReportFailure("x", errBoom) // the leader switches, the follower adopts
		st, _ := b.ChainStatus("chain")
		Expect(st.Active).To(Equal("y"))
		Expect(switchCount() - before).To(Equal(int64(1)))

		before = switchCount()
		Expect(b.Pin("chain", "x")).To(Succeed()) // applied on both frontends
		Expect(switchCount() - before).To(Equal(int64(1)))
	})
})
