package p2prelay_test

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
)

func mibps(n int64, d time.Duration) float64 { return float64(n) / d.Seconds() / (1 << 20) }

var _ = Describe("RateLimitedHost", func() {
	const size = 1 << 20

	// openRelayPair returns two peers connected through an open relay with
	// the given limits.
	openRelayPair := func(perCircuit, total int64) (a, b host.Host) {
		cfg := testConfig(true)
		cfg.RateLimit, cfg.TotalRateLimit = perCircuit, total
		r := startRelay(cfg)
		a, b = newPeer(), newPeer()
		Expect(reserve(a, r)).To(Succeed())
		Expect(circuit(b, a, r)).To(Succeed())
		return a, b
	}

	It("returns the host itself when there is no limit", func() {
		h := newPeer()
		Expect(p2prelay.RateLimitedHost(h, 0, 0)).To(BeIdenticalTo(h))
	})

	It("limits both legs of a circuit and leaves an unlimited relay fast", func() {
		a, b := openRelayPair(0, 0)
		n, free, err := download(b, a, size)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(size))
		freeUp, err := upload(b, a, size)
		Expect(err).ToNot(HaveOccurred())

		const limit = 256 << 10
		a, b = openRelayPair(limit, 0)
		n, down, err := download(b, a, size)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(size))
		up, err := upload(b, a, size)
		Expect(err).ToNot(HaveOccurred())

		GinkgoWriter.Printf("1 MiB through the relay: no limit down %v (%.1f MiB/s) up %v (%.1f MiB/s); "+
			"256 KiB/s limit down %v (%.2f MiB/s) up %v (%.2f MiB/s)\n",
			free, mibps(size, free), freeUp, mibps(size, freeUp), down, mibps(size, down), up, mibps(size, up))
		// 1 MiB at 256 KiB/s is 4 s; the first burst is at most 64 KiB.
		Expect(down).To(BeNumerically(">=", 3500*time.Millisecond))
		Expect(up).To(BeNumerically(">=", 3500*time.Millisecond))
		Expect(down).To(BeNumerically("<", 8*time.Second))
		Expect(free).To(BeNumerically("<", 2*time.Second))
		Expect(freeUp).To(BeNumerically("<", 2*time.Second))
	})

	It("shares the total limit between circuits", func() {
		const total = 256 << 10
		cfg := testConfig(true)
		// An open relay has a per-circuit limit by default; only the total
		// limit is under test here.
		cfg.RateLimit, cfg.TotalRateLimit = 0, total
		r := startRelay(cfg)
		a, b, c := newPeer(), newPeer(), newPeer()
		Expect(reserve(a, r)).To(Succeed())
		Expect(circuit(b, a, r)).To(Succeed())
		Expect(circuit(c, a, r)).To(Succeed())

		var wg sync.WaitGroup
		start := time.Now()
		for _, src := range []host.Host{b, c} {
			wg.Go(func() {
				defer GinkgoRecover()
				n, _, err := download(src, a, size/2)
				Expect(err).ToNot(HaveOccurred())
				Expect(n).To(BeEquivalentTo(size / 2))
			})
		}
		wg.Wait()
		took := time.Since(start)
		GinkgoWriter.Printf("2 x 512 KiB with a 256 KiB/s total limit: %v (%.2f MiB/s)\n", took, mibps(size, took))
		// 1 MiB at 256 KiB/s is 4 s.
		Expect(took).To(BeNumerically(">=", 3500*time.Millisecond))
		Expect(took).To(BeNumerically("<", 7*time.Second))
	})
})
