package failover

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// loopSync is an in-process StateSync that delivers every publish to all
// managers synchronously, including the publisher (like NATS echo).
type loopSync struct {
	mu    sync.Mutex
	peers []*Manager
	pins  map[string]string
}

func (l *loopSync) add(m *Manager) { l.mu.Lock(); l.peers = append(l.peers, m); l.mu.Unlock() }
func (l *loopSync) each(f func(*Manager)) {
	l.mu.Lock()
	ps := append([]*Manager(nil), l.peers...)
	l.mu.Unlock()
	for _, p := range ps {
		f(p)
	}
}
func (l *loopSync) PublishTarget(s TargetSnapshot) { l.each(func(m *Manager) { m.ApplyTarget(s) }) }
func (l *loopSync) PublishChain(s ChainSnapshot)   { l.each(func(m *Manager) { m.ApplyChain(s) }) }
func (l *loopSync) SetPin(c, t string) error {
	l.mu.Lock()
	l.pins[c] = t
	l.mu.Unlock()
	l.each(func(m *Manager) { m.ApplyPin(c, t) })
	return nil
}
func (l *loopSync) ClearPin(c string) error {
	l.mu.Lock()
	delete(l.pins, c)
	l.mu.Unlock()
	l.each(func(m *Manager) { m.ApplyPin(c, "") })
	return nil
}
func (l *loopSync) Pins() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]string{}
	for k, v := range l.pins {
		out[k] = v
	}
	return out
}

// failingPinSync is a StateSync whose pin writes fail (the DB is down).
type failingPinSync struct{ *loopSync }

func (failingPinSync) SetPin(string, string) error { return errBoom }
func (failingPinSync) ClearPin(string) error       { return errBoom }

var _ = Describe("Manager state sync", func() {
	var (
		clock     *fakeClock
		src       *fakeSource
		bus       *loopSync
		a, b      *Manager
		leaderIsA bool
		gateFor   func(isA bool) LeaderGate
		ctx       = context.Background()
	)

	BeforeEach(func() {
		clock = newFakeClock()
		src = newFakeSource(remote("x"), local("y"), chainCfg("chain", nil, t("x"), t("y")))
		bus = &loopSync{pins: map[string]string{}}
		leaderIsA = true
		gateFor = func(isA bool) LeaderGate {
			return func(_ context.Context, fn func()) bool {
				if isA != leaderIsA {
					return false
				}
				fn()
				return true
			}
		}
		a = New(src, WithClock(clock), WithLeaderGate(gateFor(true)))
		b = New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		bus.add(a)
		bus.add(b)
		a.SetStateSync(bus)
		b.SetStateSync(bus)
		a.Tick(ctx)
		b.Tick(ctx)
	})

	It("echo of own publish is a no-op and emits one event", func() {
		events, cancel := a.Subscribe(16)
		defer cancel()
		a.ReportFailure("x", errBoom)
		n := 0
		for _, e := range drain(events) {
			if e.Type == EventTargetState && e.Target == "x" {
				n++
			}
		}
		Expect(n).To(Equal(1))
	})

	It("a trip on one frontend is skipped by the other's plan", func() {
		b.ReportFailure("x", errBoom)
		att, err := a.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Target()).To(Equal("y"))
	})

	It("followers adopt the leader's chain state and emit the switch", func() {
		events, cancel := b.Subscribe(16)
		defer cancel()
		a.ReportFailure("x", errBoom) // leader recomputes and publishes chain state
		st, _ := b.ChainStatus("chain")
		Expect(st.Active).To(Equal("y"))
		var sw []Event
		for _, e := range drain(events) {
			if e.Type == EventChainSwitched {
				sw = append(sw, e)
			}
		}
		Expect(sw).ToNot(BeEmpty())
	})

	It("a pin on one frontend applies on all", func() {
		Expect(b.Pin("chain", "y")).To(Succeed())
		st, _ := a.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
		Expect(a.Unpin("chain")).To(Succeed())
		st, _ = b.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
	})

	It("hydrates pins when the sync is attached", func() {
		bus.pins["chain"] = "y"
		c := New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		c.SetStateSync(bus)
		st, _ := c.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
	})

	It("ReconcilePins converges a frontend that missed a pin and an unpin", func() {
		// The shared pin set changes without B hearing the delta, as after a
		// NATS reconnect whose re-hydrate fires no OnApply.
		bus.mu.Lock()
		bus.pins["chain"] = "y"
		bus.mu.Unlock()
		b.ReconcilePins()
		st, _ := b.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
		Expect(st.Active).To(Equal("y"))

		bus.mu.Lock()
		delete(bus.pins, "chain")
		bus.mu.Unlock()
		b.ReconcilePins()
		st, _ = b.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
	})

	It("ReconcilePins leaves a pin for a chain this frontend does not know yet", func() {
		bus.mu.Lock()
		bus.pins["later"] = "y"
		bus.mu.Unlock()
		b.ReconcilePins()
		src.Put(chainCfg("later", nil, t("x"), t("y")))
		b.Sync()
		st, ok := b.ChainStatus("later")
		Expect(ok).To(BeTrue())
		Expect(st.Pinned).ToNot(BeNil())
	})

	It("rolls a pin back when the shared write fails", func() {
		m := New(src, WithClock(clock))
		m.SetStateSync(failingPinSync{bus})
		Expect(m.Pin("chain", "y")).To(MatchError(errBoom))
		st, _ := m.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
	})

	It("restores the previous pin when a re-pin or an unpin fails to share", func() {
		m := New(src, WithClock(clock))
		m.SetStateSync(bus)
		Expect(m.Pin("chain", "y")).To(Succeed())
		m.SetStateSync(failingPinSync{bus})

		Expect(m.Pin("chain", "x")).To(MatchError(errBoom))
		st, _ := m.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))

		Expect(m.Unpin("chain")).To(MatchError(errBoom))
		st, _ = m.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
		Expect(st.Active).To(Equal("y"))
	})

	It("SetLeaderGate gates a manager built without one", func() {
		// Production builds the manager before distributed init, so the gate
		// arrives through the setter rather than the option.
		p := &fakeProber{fail: map[string]error{}}
		m := New(src, WithClock(clock), WithProber(p))
		Expect(m.IsLeader()).To(BeTrue())
		m.SetLeaderGate(gateFor(false))
		Expect(m.IsLeader()).To(BeFalse(), "a gated manager is not the leader until the gate grants it")
		m.Tick(ctx)
		Consistently(func() int { return len(p.take()) }, 200*time.Millisecond).Should(Equal(0), "a follower must not probe")
	})

	It("only the leader probes", func() {
		pa, pb := &fakeProber{fail: map[string]error{}}, &fakeProber{fail: map[string]error{}}
		a = New(src, WithClock(clock), WithProber(pa), WithLeaderGate(gateFor(true)))
		b = New(src, WithClock(clock), WithProber(pb), WithLeaderGate(gateFor(false)))
		a.SetStateSync(bus)
		b.SetStateSync(bus)
		a.Tick(ctx)
		b.Tick(ctx)
		Eventually(func() int { return len(pa.take()) }).Should(BeNumerically(">", 0))
		Consistently(func() int { return len(pb.take()) }, 200*time.Millisecond).Should(Equal(0))
		Expect(a.IsLeader()).To(BeTrue())
		Expect(b.IsLeader()).To(BeFalse())
	})

	It("new leader keeps activeSince across a leadership move", func() {
		a.ReportFailure("x", errBoom)
		before, _ := b.ChainStatus("chain")
		leaderIsA = false
		clock.Advance(5 * time.Second)
		a.Tick(ctx)
		b.Tick(ctx)
		after, _ := b.ChainStatus("chain")
		Expect(after.ActiveSince).To(Equal(before.ActiveSince))
		Expect(b.IsLeader()).To(BeTrue())
	})

	It("republish sends every target and chain", func() {
		c := New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		bus.add(c)
		c.SetStateSync(bus)
		a.ReportFailure("x", errBoom)
		a.Republish()
		st, _ := c.ChainStatus("chain")
		Expect(st.Active).To(Equal("y"))
	})

	It("keeps a pin for a chain it does not know yet and applies it when the chain appears", func() {
		b.ApplyPin("later", "y")
		_, ok := b.ChainStatus("later")
		Expect(ok).To(BeFalse())
		src.Put(chainCfg("later", nil, t("x"), t("y")))
		b.Sync()
		st, ok := b.ChainStatus("later")
		Expect(ok).To(BeTrue())
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
		Expect(st.Active).To(Equal("y"))
	})

	It("re-applies a shared pin when the chain is removed and re-added", func() {
		Expect(a.Pin("chain", "y")).To(Succeed())
		src.Delete("chain")
		b.Sync()
		_, ok := b.ChainStatus("chain")
		Expect(ok).To(BeFalse())
		src.Put(chainCfg("chain", nil, t("x"), t("y")))
		b.Sync()
		st, _ := b.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
	})

	It("applies a deferred pin once its target joins the chain", func() {
		src.Put(local("z"))
		b.ApplyPin("chain", "z")
		st, _ := b.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
		src.Put(chainCfg("chain", nil, t("x"), t("y"), t("z")))
		b.Sync()
		st, _ = b.ChainStatus("chain")
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("z"))
	})

	It("hydrates a pin for an unknown chain and applies it when the chain appears", func() {
		bus.pins["later"] = "y"
		c := New(src, WithClock(clock), WithLeaderGate(gateFor(false)))
		c.SetStateSync(bus)
		src.Put(chainCfg("later", nil, t("x"), t("y")))
		c.Sync()
		st, ok := c.ChainStatus("later")
		Expect(ok).To(BeTrue())
		Expect(st.Pinned).ToNot(BeNil())
		Expect(*st.Pinned).To(Equal("y"))
	})

	It("standalone, a removed chain drops its pin", func() {
		m := New(src, WithClock(clock))
		Expect(m.Pin("chain", "y")).To(Succeed())
		src.Delete("chain")
		m.Sync()
		src.Put(chainCfg("chain", nil, t("x"), t("y")))
		m.Sync()
		st, _ := m.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
	})

	It("standalone manager (no sync, no gate) is always leader", func() {
		m := New(src, WithClock(clock))
		m.Tick(ctx)
		Expect(m.IsLeader()).To(BeTrue())
	})
})
