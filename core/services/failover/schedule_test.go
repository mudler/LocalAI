package failover

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type probeCall struct {
	target    string
	inference bool
}

type fakeProber struct {
	mu    sync.Mutex
	calls []probeCall
	fail  map[string]error // target -> error returned by every probe
}

func (p *fakeProber) record(target string, inference bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, probeCall{target, inference})
	return p.fail[target]
}
func (p *fakeProber) Liveness(_ context.Context, c config.ModelConfig, _ Kind, _ bool) error {
	return p.record(c.Name, false)
}
func (p *fakeProber) Inference(_ context.Context, c config.ModelConfig, _ Kind, _ bool) error {
	return p.record(c.Name, true)
}
func (p *fakeProber) take() []probeCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.calls
	p.calls = nil
	return out
}

var _ = Describe("Manager probes", func() {
	var (
		clock  *fakeClock
		src    *fakeSource
		prober *fakeProber
		m      *Manager
		ctx    = context.Background()
	)

	BeforeEach(func() {
		clock = newFakeClock()
		prober = &fakeProber{fail: map[string]error{}}
		src = newFakeSource(remote("a"), local("b"), local("cold"),
			chainCfg("chain", nil, t("a"), warmT("b")))
		m = New(src, WithClock(clock), WithProber(prober))
	})

	It("probes idle targets on the first tick and not again before the interval", func() {
		m.Tick(ctx)
		Expect(prober.take()).To(ConsistOf(probeCall{"a", false}, probeCall{"b", false}))
		clock.Advance(5 * time.Second)
		m.Tick(ctx)
		Expect(prober.take()).To(BeEmpty())
	})

	It("skips the liveness probe for a target with recent traffic", func() {
		m.Tick(ctx)
		prober.take()
		clock.Advance(14 * time.Second)
		m.ReportSuccess("a")
		clock.Advance(2 * time.Second)
		m.Tick(ctx)
		Expect(prober.take()).To(ConsistOf(probeCall{"b", false}))
	})

	It("trips a target whose liveness probe fails", func() {
		prober.fail["a"] = errBoom
		m.Tick(ctx)
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
		Expect(st.Active).To(Equal("b"))
	})

	It("recovers through liveness, then inference probes, then fails back after dwell", func() {
		prober.fail["a"] = errBoom
		m.Tick(ctx)
		delete(prober.fail, "a")
		prober.take()

		clock.Advance(15 * time.Second)
		m.Tick(ctx) // liveness passes: recovering
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateRecovering))

		for i := 0; i < 3; i++ {
			clock.Advance(15 * time.Second)
			m.Tick(ctx)
		}
		calls := prober.take()
		Expect(calls).To(ContainElement(probeCall{"a", true}))
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
		Expect(st.Active).To(Equal("a"), "60s min_dwell passed during the 4 ticks")
	})

	It("sends a recovering target back down when an inference probe fails", func() {
		m.ReportFailure("a", errBoom)
		clock.Advance(15 * time.Second)
		m.Tick(ctx) // liveness passes: recovering
		prober.fail["a"] = errBoom
		clock.Advance(15 * time.Second)
		m.Tick(ctx)
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
	})

	It("never probes a down cold target and restores it after min_dwell", func() {
		src.Put(chainCfg("chain", nil, t("cold"), warmT("b")))
		m.Sync()
		m.ReportFailure("cold", errBoom)
		prober.take()
		clock.Advance(30 * time.Second)
		m.Tick(ctx)
		for _, c := range prober.take() {
			Expect(c.target).ToNot(Equal("cold"))
		}
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
		clock.Advance(31 * time.Second)
		m.Tick(ctx)
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
	})

	It("probes a target shared by two chains once per tick", func() {
		src.Put(chainCfg("chain2", nil, t("a"), warmT("b")))
		m.Tick(ctx)
		calls := prober.take()
		n := 0
		for _, c := range calls {
			if c.target == "a" {
				n++
			}
		}
		Expect(n).To(Equal(1))
	})

	It("closes subscriptions when Run stops", func() {
		events, _ := m.Subscribe(1)
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { m.Run(rctx); close(done) }()
		cancel()
		Eventually(done).Should(BeClosed())
		Eventually(events).Should(BeClosed())
	})
})
