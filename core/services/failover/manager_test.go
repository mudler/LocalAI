package failover

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
)

var errBoom = errors.New("dial tcp: connection refused")

var _ = Describe("Manager", func() {
	var (
		clock *fakeClock
		src   *fakeSource
		m     *Manager
	)

	BeforeEach(func() {
		clock = newFakeClock()
		src = newFakeSource(remote("a"), local("b"), chainCfg("chain", nil, t("a"), t("b")))
		m = New(src, WithClock(clock))
	})

	switched := func(evs []Event) []Event {
		var out []Event
		for _, e := range evs {
			if e.Type == EventChainSwitched {
				out = append(out, e)
			}
		}
		return out
	}

	It("plans the primary first on a fresh chain", func() {
		att, err := m.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Target()).To(Equal("a"))
		Expect(att.Primary()).To(Equal("a"))
		Expect(att.Degraded()).To(BeFalse())
		st, ok := m.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(st.State).To(Equal(ChainPrimary))
		Expect(st.Targets[0].Kind).To(Equal(KindRemote))
		Expect(st.Targets[1].Kind).To(Equal(KindLocal))
	})

	It("skips to the next target without recording a failure", func() {
		att, _ := m.Plan("chain")
		Expect(att.Skip()).To(BeTrue())
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Skip()).To(BeFalse())
		Expect(att.Target()).To(Equal("b"))
		st, _ := m.ChainStatus("chain")
		Expect(st.Active).To(Equal("a"))
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
	})

	It("reports whether any chain was configured at the last sync", func() {
		m.Sync()
		Expect(m.HasChains()).To(BeTrue())
		var nilManager *Manager
		Expect(nilManager.HasChains()).To(BeFalse())
	})

	It("answers HasChains from the last sync without scanning the config source", func() {
		empty := New(newFakeSource(remote("a")), WithClock(clock))
		Expect(empty.HasChains()).To(BeFalse())
		lateSrc := newFakeSource(remote("a"), local("b"))
		late := New(lateSrc, WithClock(clock))
		late.Sync()
		scans := lateSrc.Scans()
		for range 100 {
			Expect(late.HasChains()).To(BeFalse())
		}
		Expect(lateSrc.Scans()).To(Equal(scans))
		// A chain added since the last sync is seen at the next sync, or
		// sooner by Plan, which syncs on a miss.
		lateSrc.Put(chainCfg("chain", nil, t("a"), t("b")))
		Expect(late.HasChains()).To(BeFalse())
		_, err := late.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(late.HasChains()).To(BeTrue())
		lateSrc.Delete("chain")
		late.Sync()
		Expect(late.HasChains()).To(BeFalse())
	})

	It("returns ErrChainNotFound for an unknown chain", func() {
		_, err := m.Plan("nope")
		Expect(errors.Is(err, ErrChainNotFound)).To(BeTrue())
	})

	It("trips on the first failure by default and switches with an event", func() {
		events, cancel := m.Subscribe(16)
		defer cancel()
		att, _ := m.Plan("chain")
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Target()).To(Equal("b"))
		st, _ := m.ChainStatus("chain")
		Expect(st.Active).To(Equal("b"))
		Expect(st.State).To(Equal(ChainFallback))
		Expect(st.Targets[0].State).To(Equal(StateDown))
		Expect(st.Targets[0].LastError).To(ContainSubstring("connection refused"))
		sw := switched(drain(events))
		Expect(sw).To(HaveLen(1))
		Expect(sw[0]).To(MatchFields(IgnoreExtras, Fields{
			"Chain": Equal("chain"), "From": Equal("a"), "To": Equal("b"),
			"State": Equal("fallback"), "Reason": Equal(ReasonTrip),
		}))
	})

	It("counts failures inside the trip window only", func() {
		src.Put(chainCfg("chain", &config.FailoverConfig{Trip: config.FailoverTrip{Errors: 2, Window: "30s"}}, t("a"), t("b")))
		m.Sync()
		m.ReportFailure("a", errBoom)
		clock.Advance(31 * time.Second)
		m.ReportFailure("a", errBoom)
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
		clock.Advance(time.Second)
		m.ReportFailure("a", errBoom)
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
	})

	It("fails back only after recovery probes and min_dwell", func() {
		_, _ = m.Plan("chain")
		m.ReportFailure("a", errBoom)
		for i := 0; i < 3; i++ {
			m.ReportSuccess("a") // a real success counts like a passed inference probe
		}
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
		Expect(st.Active).To(Equal("b"), "min_dwell has not passed")
		clock.Advance(61 * time.Second)
		events, cancel := m.Subscribe(16)
		defer cancel()
		m.Reevaluate()
		st, _ = m.ChainStatus("chain")
		Expect(st.Active).To(Equal("a"))
		Expect(switched(drain(events))[0].Reason).To(Equal(ReasonRecovery))
	})

	It("moves up at once when the active target itself goes down", func() {
		src.Put(chainCfg("chain", nil, t("a"), t("b"), t("c")))
		src.Put(local("c"))
		m.Sync()
		m.ReportFailure("a", errBoom) // active: b
		for i := 0; i < 3; i++ {
			m.ReportSuccess("a") // a healthy again, but dwell not passed
		}
		m.ReportFailure("b", errBoom) // b down: go to a now, not c
		st, _ := m.ChainStatus("chain")
		Expect(st.Active).To(Equal("a"))
	})

	It("goes degraded when all targets are down and plans all of them in priority order", func() {
		m.ReportFailure("a", errBoom)
		m.ReportFailure("b", errBoom)
		st, _ := m.ChainStatus("chain")
		Expect(st.State).To(Equal(ChainDegraded))
		att, err := m.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Degraded()).To(BeTrue())
		Expect(att.Target()).To(Equal("a"))
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Fail(errBoom)).To(BeFalse())
	})

	It("emits chain.switched when leaving degraded without an active-target change", func() {
		m.ReportFailure("a", errBoom) // active moves to b
		m.ReportFailure("b", errBoom) // both down: degraded, active stays b
		st, _ := m.ChainStatus("chain")
		Expect(st.State).To(Equal(ChainDegraded))
		Expect(st.Active).To(Equal("b"))
		events, cancel := m.Subscribe(16)
		defer cancel()
		m.ReportSuccess("b") // b is cold local: one success recovers it in place
		st, _ = m.ChainStatus("chain")
		Expect(st.State).To(Equal(ChainFallback))
		Expect(st.Active).To(Equal("b"), "the active target itself recovered, no switch needed")
		sw := switched(drain(events))
		Expect(sw).To(HaveLen(1), "leaving degraded must still notify chain.switched listeners")
		Expect(sw[0]).To(MatchFields(IgnoreExtras, Fields{
			"Chain": Equal("chain"), "State": Equal("fallback"), "Reason": Equal(ReasonRecovery),
		}))
	})

	It("pins a target regardless of health", func() {
		Expect(m.Pin("chain", "b")).To(Succeed())
		att, _ := m.Plan("chain")
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Fail(errBoom)).To(BeFalse(), "a pin allows only the pinned target")
		st, _ := m.ChainStatus("chain")
		Expect(*st.Pinned).To(Equal("b"))
		Expect(st.Active).To(Equal("b"))
		Expect(m.Unpin("chain")).To(Succeed())
		st, _ = m.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
		Expect(errors.Is(m.Pin("chain", "zzz"), ErrTargetNotInChain)).To(BeTrue())
		Expect(errors.Is(m.Pin("nope", "a"), ErrChainNotFound)).To(BeTrue())
	})

	It("shares target health across chains", func() {
		src.Put(chainCfg("chain2", nil, t("a"), t("b")))
		m.Sync()
		m.ReportFailure("a", errBoom)
		s1, _ := m.ChainStatus("chain")
		s2, _ := m.ChainStatus("chain2")
		Expect(s1.Active).To(Equal("b"))
		Expect(s2.Active).To(Equal("b"))
	})

	It("marks a removed target missing and leaves it out of plans", func() {
		_, _ = m.Plan("chain")
		src.Delete("a")
		m.Sync()
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateMissing))
		att, _ := m.Plan("chain")
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Fail(errBoom)).To(BeFalse())
	})

	It("resets a chain whose target list changed", func() {
		m.ReportFailure("a", errBoom)
		src.Put(local("c"))
		src.Put(chainCfg("chain", nil, t("c"), t("b")))
		m.Sync()
		st, _ := m.ChainStatus("chain")
		Expect(st.Active).To(Equal("c"))
	})

	It("reports warm local targets and ignores warm on remote ones", func() {
		var got []string
		m = New(src, WithClock(clock), WithOnWarmChanged(func(w []string) { got = w }))
		src.Put(chainCfg("chain", nil, warmT("a"), warmT("b")))
		m.Sync()
		Expect(got).To(Equal([]string{"b"}))
		Expect(m.WarmTargets()).To(Equal([]string{"b"}))
	})

	It("closes a subscription on cancel", func() {
		events, cancel := m.Subscribe(1)
		cancel()
		_, ok := <-events
		Expect(ok).To(BeFalse())
		cancel() // idempotent
	})

	Describe("Do", func() {
		It("retries on the next target until commit", func() {
			var tried []string
			err := m.Do(context.Background(), "chain", func(_ context.Context, target string, commit func()) error {
				tried = append(tried, target)
				if target == "a" {
					return errBoom
				}
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(tried).To(Equal([]string{"a", "b"}))
		})

		It("does not retry after commit but still trips the target", func() {
			var tried []string
			err := m.Do(context.Background(), "chain", func(_ context.Context, target string, commit func()) error {
				tried = append(tried, target)
				commit()
				return errBoom
			})
			Expect(err).To(MatchError(errBoom))
			Expect(tried).To(Equal([]string{"a"}))
			st, _ := m.ChainStatus("chain")
			Expect(st.Targets[0].State).To(Equal(StateDown))
		})

		It("does not retry or trip on a non-retryable error", func() {
			bad := errors.New("the request exceeds the available context size")
			err := m.Do(context.Background(), "chain", func(_ context.Context, _ string, _ func()) error { return bad })
			Expect(err).To(MatchError(bad))
			st, _ := m.ChainStatus("chain")
			Expect(st.Targets[0].State).To(Equal(StateHealthy))
		})
	})
})
