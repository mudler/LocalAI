package failover

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Balanced chains", func() {
	var (
		clock *fakeClock
		src   *fakeSource
	)

	balanced := func(p config.FailoverPolicy, targets ...config.FailoverTarget) config.ModelConfig {
		return chainCfg("group", &config.FailoverConfig{Policy: p}, targets...)
	}
	drainT := func(model string) config.FailoverTarget {
		return config.FailoverTarget{Model: model, Drain: true}
	}
	// firsts plans n requests that finish at once and returns the target
	// each one started on.
	firsts := func(m *Manager, n int) []string {
		var out []string
		for range n {
			att, err := m.Plan("group")
			Expect(err).ToNot(HaveOccurred())
			out = append(out, att.Target())
			att.Succeed()
			att.Release()
		}
		return out
	}

	BeforeEach(func() {
		clock = newFakeClock()
		src = newFakeSource(local("a"), remote("b"), remote("c"))
	})

	It("serves each healthy target in turn with round robin", func() {
		src.Put(balanced(config.FailoverPolicyRoundRobin, t("a"), t("b"), t("c")))
		m := New(src, WithClock(clock))
		Expect(firsts(m, 6)).To(Equal([]string{"a", "b", "c", "a", "b", "c"}))
	})

	It("sends a request to the target with the fewest requests in flight", func() {
		src.Put(balanced(config.FailoverPolicyLeastBusy, t("a"), t("b"), t("c")))
		m := New(src, WithClock(clock))
		held := []*Attempt{}
		for range 3 {
			att, err := m.Plan("group")
			Expect(err).ToNot(HaveOccurred())
			held = append(held, att)
		}
		// Three requests in flight, one on each target.
		Expect([]string{held[0].Target(), held[1].Target(), held[2].Target()}).To(ConsistOf("a", "b", "c"))
		// b finishes: the next request goes to b, the only idle target.
		for _, att := range held {
			if att.Target() == "b" {
				att.Release()
			}
		}
		next, err := m.Plan("group")
		Expect(err).ToNot(HaveOccurred())
		Expect(next.Target()).To(Equal("b"))

		st, _ := m.ChainStatus("group")
		Expect(st.Policy).To(Equal(config.FailoverPolicyLeastBusy))
		inFlight := map[string]int{}
		for _, ts := range st.Targets {
			inFlight[ts.Model] = ts.InFlight
		}
		Expect(inFlight).To(Equal(map[string]int{"a": 1, "b": 1, "c": 1}))
	})

	It("counts a request once however often it moves and releases it once", func() {
		src.Put(balanced(config.FailoverPolicyLeastBusy, t("a"), t("b")))
		m := New(src, WithClock(clock))
		att, _ := m.Plan("group")
		first := att.Target()
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Target()).ToNot(Equal(first))
		att.Release()
		att.Release()
		st, _ := m.ChainStatus("group")
		for _, ts := range st.Targets {
			Expect(ts.InFlight).To(Equal(0), ts.Model)
		}
	})

	It("moves a failed request to the other targets and trips the failed one", func() {
		src.Put(balanced(config.FailoverPolicyRoundRobin, t("a"), t("b")))
		m := New(src, WithClock(clock))
		att, _ := m.Plan("group")
		Expect(att.Target()).To(Equal("a"))
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Target()).To(Equal("b"))
		att.Succeed()
		att.Release()
		// a is down: every new request goes to b until a recovers.
		Expect(firsts(m, 3)).To(Equal([]string{"b", "b", "b"}))
	})

	It("never starts a request on a drained target but keeps its health", func() {
		src.Put(balanced(config.FailoverPolicyRoundRobin, t("a"), drainT("b"), t("c")))
		m := New(src, WithClock(clock))
		Expect(firsts(m, 4)).To(Equal([]string{"a", "c", "a", "c"}))
		st, _ := m.ChainStatus("group")
		Expect(st.Targets[1].Drained).To(BeTrue())
		Expect(st.Targets[1].State).To(Equal(StateHealthy))
	})

	It("drains in a priority chain too", func() {
		src.Put(chainCfg("group", nil, drainT("a"), t("b")))
		m := New(src, WithClock(clock))
		att, err := m.Plan("group")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Fail(errBoom)).To(BeFalse())
	})

	It("tries a constrained target only after the others", func() {
		src.Put(balanced(config.FailoverPolicyRoundRobin, t("a"), t("b"), t("c")))
		m := New(src, WithClock(clock), WithConstrained(func(c config.ModelConfig) bool { return c.Name == "a" }))
		Expect(firsts(m, 4)).To(Equal([]string{"b", "c", "b", "c"}))
		att, _ := m.Plan("group")
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Target()).To(Equal("a"))
		att.Release()
		st, _ := m.ChainStatus("group")
		Expect(st.Targets[0].Constrained).To(BeTrue())
	})

	It("tries every target that is not drained when the chain is degraded", func() {
		src.Put(balanced(config.FailoverPolicyLeastBusy, t("a"), t("b"), drainT("c")))
		m := New(src, WithClock(clock))
		m.ReportFailure("a", errBoom)
		m.ReportFailure("b", errBoom)
		att, err := m.Plan("group")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Degraded()).To(BeTrue())
		Expect(att.Target()).To(BeElementOf("a", "b"))
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Fail(errBoom)).To(BeFalse())
	})
})
