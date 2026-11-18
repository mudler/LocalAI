package p2prelay_test

import (
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

func i64(n int64) *int64 { return &n }

// setLimits sends OpLimits with a patch, or only reads when patch is nil.
func setLimits(h host.Host, r *testRelay, patch *relayproto.LimitsPatch) relayproto.Response {
	resp, err := call(h, r, relayproto.Request{Op: relayproto.OpLimits, Limits: patch})
	if resp.V == 0 {
		// A refusal still carries a response; only a failed call does not.
		Expect(err).ToNot(HaveOccurred())
	}
	return resp
}

var _ = Describe("relay limits admin operation", func() {
	var (
		admin, member host.Host
		r             *testRelay
	)

	BeforeEach(func() {
		admin, member = newPeer(), newPeer()
		r = startRelay(testConfig(false, admin))
		Expect(push(admin, r, relayproto.OpReplace, member).Count).To(Equal(1))
	})

	It("reads the limits the relay runs with", func() {
		resp := setLimits(admin, r, nil)
		Expect(resp.OK).To(BeTrue(), resp.Err)
		Expect(resp.Limits).To(Equal(&relayproto.Limits{
			CircuitMaxBytes:   p2prelay.DefaultCircuitMaxBytes,
			CircuitMaxSeconds: int64(p2prelay.DefaultCircuitMaxDuration / time.Second),
		}))
	})

	It("changes the limits and reports them in the next reservation and in the status", func() {
		resp := setLimits(admin, r, &relayproto.LimitsPatch{
			CircuitMaxBytes: i64(5 << 20), CircuitMaxSeconds: i64(90), RateBPS: i64(512 << 10),
		})
		Expect(resp.OK).To(BeTrue(), resp.Err)
		Expect(resp.Limits.CircuitMaxBytes).To(BeEquivalentTo(5 << 20))
		Expect(resp.Limits.CircuitMaxSeconds).To(BeEquivalentTo(90))
		Expect(resp.Limits.RateBPS).To(BeEquivalentTo(512 << 10))
		Expect(resp.Limits.Restarted).To(BeTrue())

		Expect(connect(admin, r)).To(Succeed())
		rsv, err := client.Reserve(ctxT(10*time.Second), admin, r.info())
		Expect(err).ToNot(HaveOccurred())
		Expect(rsv.LimitData).To(BeEquivalentTo(5 << 20))
		Expect(rsv.LimitDuration).To(Equal(90 * time.Second))

		st := r.Status()
		Expect(st.CircuitMaxBytes).To(BeEquivalentTo(5 << 20))
		Expect(st.CircuitMaxSeconds).To(BeEquivalentTo(90))
		Expect(st.RateBPS).To(BeEquivalentTo(512 << 10))
	})

	It("keeps what it does not name", func() {
		resp := setLimits(admin, r, &relayproto.LimitsPatch{RateBPS: i64(64 << 10)})
		Expect(resp.OK).To(BeTrue(), resp.Err)
		Expect(resp.Limits.CircuitMaxBytes).To(BeEquivalentTo(p2prelay.DefaultCircuitMaxBytes))
		Expect(resp.Limits.RateBPS).To(BeEquivalentTo(64 << 10))
	})

	It("applies a bandwidth change to new circuits without a restart", func() {
		Expect(reserve(admin, r)).To(Succeed())
		resp := setLimits(admin, r, &relayproto.LimitsPatch{RateBPS: i64(128 << 10)})
		Expect(resp.OK).To(BeTrue(), resp.Err)
		Expect(resp.Limits.Restarted).To(BeFalse())
		Expect(connected(admin, r.h)).To(BeTrue())

		Expect(circuit(member, admin, r)).To(Succeed())
		_, took, err := download(member, admin, 256<<10)
		Expect(err).ToNot(HaveOccurred())
		// 256 KiB at 128 KiB/s is 2 s, less the first burst of 32 KiB.
		Expect(took).To(BeNumerically(">=", 1500*time.Millisecond))
	})

	It("removes a limit with zero", func() {
		resp := setLimits(admin, r, &relayproto.LimitsPatch{CircuitMaxBytes: i64(0), CircuitMaxSeconds: i64(0)})
		Expect(resp.OK).To(BeTrue(), resp.Err)
		Expect(connect(admin, r)).To(Succeed())
		rsv, err := client.Reserve(ctxT(10*time.Second), admin, r.info())
		Expect(err).ToNot(HaveOccurred())
		Expect(rsv.LimitData).To(BeZero())
		Expect(rsv.LimitDuration).To(BeZero())
	})

	It("refuses a negative value and changes nothing", func() {
		resp := setLimits(admin, r, &relayproto.LimitsPatch{RateBPS: i64(-1)})
		Expect(resp.OK).To(BeFalse())
		Expect(resp.Err).To(ContainSubstring("negative"))
		Expect(setLimits(admin, r, nil).Limits.RateBPS).To(BeZero())
	})

	It("lets a circuit work again after the restart once the peers reserve", func() {
		Expect(reserve(admin, r)).To(Succeed())
		Expect(setLimits(admin, r, &relayproto.LimitsPatch{CircuitMaxBytes: i64(2 << 20)}).OK).To(BeTrue())
		Eventually(func() error { return reserve(admin, r) }, 10*time.Second, 200*time.Millisecond).Should(Succeed())
		Expect(circuit(member, admin, r)).To(Succeed())
		n, _, err := download(member, admin, 1<<20)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(1 << 20))
	})

	It("refuses a member and does not change the limits", func() {
		resp := setLimits(member, r, &relayproto.LimitsPatch{RateBPS: i64(1)})
		Expect(resp.OK).To(BeFalse())
		Expect(resp.Err).To(Equal("not an admin"))
		Expect(r.Status().RateBPS).To(BeZero())
	})

	It("keeps the limits over a relay restart, ahead of the flags", func() {
		Expect(setLimits(admin, r, &relayproto.LimitsPatch{
			CircuitMaxBytes: i64(7 << 20), CircuitMaxSeconds: i64(45), RateBPS: i64(100 << 10),
		}).OK).To(BeTrue())
		r.stop()

		cfg := testConfig(false, admin)
		cfg.DataDir = r.cfg.DataDir
		r2 := startRelayWithKey(cfg, r.key)
		st := r2.Status()
		Expect(st.CircuitMaxBytes).To(BeEquivalentTo(7 << 20))
		Expect(st.CircuitMaxSeconds).To(BeEquivalentTo(45))
		Expect(st.RateBPS).To(BeEquivalentTo(100 << 10))
		Expect(r2.Banner().Limits).To(ContainSubstring("7 MiB"))
	})
})
