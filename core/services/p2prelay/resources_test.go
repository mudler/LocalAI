package p2prelay

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/network"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// openAndClose opens and closes n inbound connections from addr and returns
// how many the resource manager accepted.
func openAndClose(rm network.ResourceManager, addr string, n int) int {
	endpoint := ma.StringCast(addr)
	accepted := 0
	for i := 0; i < n; i++ {
		scope, err := rm.OpenConnection(network.DirInbound, true, endpoint)
		if err != nil {
			continue
		}
		scope.Done()
		accepted++
	}
	return accepted
}

var _ = Describe("the connection rate of the relay host", func() {
	const shared = "/ip4/203.0.113.7/tcp/4001"

	newRM := func(mutate func(*Config)) network.ResourceManager {
		cfg := DefaultConfig(false)
		cfg.DataDir = GinkgoT().TempDir()
		if mutate != nil {
			mutate(&cfg)
		}
		rm, err := newResourceManager(&cfg, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(rm.Close)
		return rm
	}

	It("accepts a burst of new connections from one shared address", func() {
		// An office or a household behind one address: a few dozen devices
		// that all redeem an invite or reconnect at the same moment.
		rm := newRM(nil)
		Expect(openAndClose(rm, shared, 60)).To(Equal(60))
	})

	It("keeps the exempt loopback address unlimited", func() {
		rm := newRM(nil)
		Expect(openAndClose(rm, "/ip4/127.0.0.1/tcp/4001", 5000)).To(Equal(5000))
	})

	It("refuses a flood from one address above the burst", func() {
		rm := newRM(func(c *Config) { c.ConnBurstPerIP = 20; c.ConnRatePerIP = 0.001 })
		got := openAndClose(rm, shared, 100)
		Expect(got).To(Equal(20))
		// Another address is not affected.
		Expect(openAndClose(rm, "/ip4/203.0.113.8/tcp/4001", 10)).To(Equal(10))
	})

	It("refuses a flood from many addresses above the global ceiling", func() {
		rm := newRM(func(c *Config) { c.ConnBurstTotal = 50; c.ConnRateTotal = 0.001 })
		got := 0
		for i := 1; i <= 200; i++ {
			got += openAndClose(rm, fmt.Sprintf("/ip4/198.51.100.%d/tcp/4001", i), 1)
		}
		Expect(got).To(Equal(50))
	})

	It("removes the limits when they are zero", func() {
		rm := newRM(func(c *Config) { c.ConnRatePerIP, c.ConnBurstPerIP, c.ConnRateTotal, c.ConnBurstTotal = 0, 0, 0, 0 })
		Expect(openAndClose(rm, shared, 2000)).To(Equal(2000))
	})

	It("rejects a rate without a burst", func() {
		cfg := DefaultConfig(false)
		cfg.DataDir = GinkgoT().TempDir()
		cfg.ConnBurstPerIP = 0
		Expect(cfg.validate()).To(MatchError(ContainSubstring("burst")))
	})
})
