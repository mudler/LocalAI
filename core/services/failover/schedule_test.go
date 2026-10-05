package failover

import (
	"context"
	"slices"
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
	fail  map[string]error         // target -> error returned by every probe
	block map[string]chan struct{} // target -> probes wait until it is closed
}

func (p *fakeProber) record(target string, inference bool) error {
	p.mu.Lock()
	p.calls = append(p.calls, probeCall{target, inference})
	err, block := p.fail[target], p.block[target]
	p.mu.Unlock()
	if block != nil {
		<-block
	}
	return err
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
		prober = &fakeProber{fail: map[string]error{}, block: map[string]chan struct{}{}}
		src = newFakeSource(remote("a"), local("b"), local("cold"),
			chainCfg("chain", nil, t("a"), warmT("b")))
		m = New(src, WithClock(clock), WithProber(prober))
	})

	// tick runs one scheduler pass and waits for the probes it started, so
	// each spec sees their results.
	tick := func() {
		m.Tick(ctx)
		m.waitProbes()
	}

	It("probes idle targets on the first tick and not again before the interval", func() {
		tick()
		Expect(prober.take()).To(ConsistOf(probeCall{"a", false}, probeCall{"b", false}))
		clock.Advance(5 * time.Second)
		tick()
		Expect(prober.take()).To(BeEmpty())
	})

	It("skips the liveness probe for a target with recent traffic", func() {
		tick()
		prober.take()
		clock.Advance(14 * time.Second)
		m.ReportSuccess("a")
		clock.Advance(2 * time.Second)
		tick()
		Expect(prober.take()).To(ConsistOf(probeCall{"b", false}))
	})

	It("trips a target whose liveness probe fails", func() {
		prober.fail["a"] = errBoom
		tick()
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
		Expect(st.Active).To(Equal("b"))
	})

	It("recovers through liveness, then inference probes, then fails back after dwell", func() {
		prober.fail["a"] = errBoom
		tick()
		delete(prober.fail, "a")
		prober.take()

		clock.Advance(15 * time.Second)
		tick() // liveness passes: recovering
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateRecovering))

		for i := 0; i < 3; i++ {
			clock.Advance(15 * time.Second)
			tick()
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
		tick() // liveness passes: recovering
		prober.fail["a"] = errBoom
		clock.Advance(15 * time.Second)
		tick()
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
	})

	It("never probes a down cold target and restores it after min_dwell", func() {
		src.Put(chainCfg("chain", nil, t("cold"), warmT("b")))
		m.Sync()
		m.ReportFailure("cold", errBoom)
		prober.take()
		clock.Advance(30 * time.Second)
		tick()
		for _, c := range prober.take() {
			Expect(c.target).ToNot(Equal("cold"))
		}
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
		clock.Advance(31 * time.Second)
		tick()
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
	})

	It("probes a target shared by two chains once per tick", func() {
		src.Put(chainCfg("chain2", nil, t("a"), warmT("b")))
		tick()
		calls := prober.take()
		n := 0
		for _, c := range calls {
			if c.target == "a" {
				n++
			}
		}
		Expect(n).To(Equal(1))
	})

	It("does not hold other targets' probes behind a slow one", func() {
		src.Put(remote("c"))
		src.Put(chainCfg("chain2", nil, t("c"), warmT("b")))
		release := make(chan struct{})
		DeferCleanup(func() { close(release); m.waitProbes() })
		prober.block["a"] = release
		m.ReportFailure("c", errBoom)
		prober.take()

		m.Tick(ctx) // a hangs; c's liveness still runs and starts its recovery
		Eventually(func() TargetState {
			st, _ := m.ChainStatus("chain2")
			return st.Targets[0].State
		}).Should(Equal(StateRecovering))

		clock.Advance(15 * time.Second)
		m.Tick(ctx) // a is still in flight: no second probe for it
		Eventually(func() []probeCall { return prober.take() }).Should(ContainElement(probeCall{"c", true}))
	})

	It("does not probe a target again while its probe is in flight", func() {
		release := make(chan struct{})
		prober.block["a"] = release
		m.Tick(ctx)
		Eventually(func() []probeCall {
			prober.mu.Lock()
			defer prober.mu.Unlock()
			return slices.Clone(prober.calls)
		}).Should(ContainElement(probeCall{"a", false}))
		for range 3 {
			clock.Advance(15 * time.Second)
			m.Tick(ctx)
		}
		close(release)
		m.waitProbes()
		n := 0
		for _, c := range prober.take() {
			if c.target == "a" {
				n++
			}
		}
		Expect(n).To(Equal(1))
	})

	It("restores a warm target that is not loaded like a cold one, after min_dwell", func() {
		m.ReportFailure("b", errBoom)
		prober.fail["b"] = ErrNotLoaded
		prober.take()
		clock.Advance(15 * time.Second)
		prober.fail["b"] = nil
		tick() // liveness passes: recovering
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[1].State).To(Equal(StateRecovering))
		prober.fail["b"] = ErrNotLoaded
		clock.Advance(15 * time.Second)
		tick() // nothing to confirm against yet, and no trip
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[1].State).To(Equal(StateRecovering))
		clock.Advance(31 * time.Second)
		tick()
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[1].State).To(Equal(StateHealthy))
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
