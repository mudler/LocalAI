// SPDX-License-Identifier: MIT
package diagnostics

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Diagnostics configuration", func() {
	It("defaults to inert diagnostics", func() {
		Expect(DefaultOptions()).To(Equal(Options{Address: "127.0.0.1:6060"}))
		Expect(DefaultOptions().Validate()).To(Succeed())
	})
	It("accepts numeric loopback forms and decimal boundary ports", func() {
		for _, address := range []string{"127.0.0.1:1", "127.255.255.254:65535", "[::1]:6060", "[0:0:0:0:0:0:0:1]:6060", "[::ffff:127.0.0.1]:6060", "127.0.0.1:06060"} {
			o := DefaultOptions()
			o.Address = address
			Expect(o.Validate()).To(Succeed(), address)
		}
	})
	It("rejects invalid addresses even when disabled", func() {
		for _, address := range []string{"", ":6060", "localhost:6060", "0.0.0.0:6060", "[::]:6060", "192.168.1.2:6060", "8.8.8.8:6060", "[::1%lo]:6060", "unix:/tmp/profile", "127.0.0.1", "::1:6060", "127.0.0.1:", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:999999999999999999999999", "127.0.0.1:-1", "127.0.0.1:+1", "127.0.0.1:1.0", "127.0.0.1:http", "127.0.0.1: 1", "127.1:6060", "127.000.0.1:6060", "[::ffff:192.168.1.2]:6060"} {
			o := DefaultOptions()
			o.Address = address
			Expect(o.Validate()).NotTo(Succeed(), address)
		}
	})
	It("validates sampling independently from timing", func() {
		for _, rates := range [][2]int{{-1, 0}, {0, -1}, {1, 0}, {0, 1}} {
			o := DefaultOptions()
			o.RequestPhaseTiming = true
			o.MutexProfileFraction = rates[0]
			o.BlockProfileRate = rates[1]
			Expect(o.Validate()).NotTo(Succeed())
			o.Pprof = true
			if rates[0] >= 0 && rates[1] >= 0 {
				Expect(o.Validate()).To(Succeed())
			} else {
				Expect(o.Validate()).NotTo(Succeed())
			}
		}
	})
})
