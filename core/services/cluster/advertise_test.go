package cluster_test

import (
	"net"

	"github.com/mudler/LocalAI/core/services/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Advertised address discovery", func() {
	// The address itself depends on host networking and is deliberately not
	// asserted. What is portable is the shape: whatever interface routes to the
	// database, the port must be the one the caller asked for, not the
	// database's.
	It("combines a local interface with the caller's port", func() {
		addr, err := cluster.DiscoverAdvertisedAddr("postgres://198.51.100.1:5432/testdb", 8080)
		if err != nil {
			Skip("no route to a database host on this machine: " + err.Error())
		}
		host, port, splitErr := net.SplitHostPort(addr)
		Expect(splitErr).ToNot(HaveOccurred())
		Expect(port).To(Equal("8080"))
		Expect(net.ParseIP(host)).ToNot(BeNil())
	})

	It("refuses a DSN it cannot derive an address from", func() {
		_, err := cluster.DiscoverAdvertisedAddr("", 8080)
		Expect(err).To(HaveOccurred())
	})

	// A DSN that NAMES loopback routes over loopback on every platform, so this
	// is deterministic rather than host-dependent. Co-location is not the
	// trigger: compose's `host=postgres` resolves to a bridge address and
	// discovery works there. Returning 127.0.0.1 would make a peer dialling
	// this replica reach itself.
	It("refuses a loopback route instead of advertising an address peers cannot use", func() {
		addr, err := cluster.DiscoverAdvertisedAddr("postgres://user@127.0.0.1:5432/testdb", 8080)
		Expect(addr).To(BeEmpty())
		Expect(err).To(MatchError(ContainSubstring("loopback")))
	})

	It("refuses a port that cannot be dialled", func() {
		_, err := cluster.DiscoverAdvertisedAddr("postgres://198.51.100.1:5432/testdb", 0)
		Expect(err).To(MatchError(ContainSubstring("out of range")))
	})
})

var _ = Describe("Checking a configured advertised address", func() {
	// The configured address bypasses discovery entirely, so it bypasses every
	// rejection discovery makes. These are the checks that put back the ones
	// that can be made without a route to look at.
	It("accepts an address on a network other hosts can reach", func() {
		reason, err := cluster.CheckAdvertisedAddr("10.0.0.7:8080")
		Expect(err).ToNot(HaveOccurred())
		Expect(reason).To(BeEmpty())
	})

	It("accepts a name, because the dialler is what resolves it", func() {
		reason, err := cluster.CheckAdvertisedAddr("localai-frontend.default.svc:8080")
		Expect(err).ToNot(HaveOccurred())
		Expect(reason).To(BeEmpty())
	})

	It("refuses an address with no port, which nothing could dial", func() {
		_, err := cluster.CheckAdvertisedAddr("10.0.0.7")
		Expect(err).To(HaveOccurred())
	})

	It("refuses a port outside the dialable range", func() {
		_, err := cluster.CheckAdvertisedAddr("10.0.0.7:0")
		Expect(err).To(MatchError(ContainSubstring("port")))
	})

	It("refuses an address that names no host", func() {
		_, err := cluster.CheckAdvertisedAddr(":8080")
		Expect(err).To(MatchError(ContainSubstring("no host")))
	})

	It("reports loopback without refusing it, because one host is a supported topology", func() {
		// Correct on a single host, and the value most likely to be copied
		// onto three, where every peer would then dial itself.
		reason, err := cluster.CheckAdvertisedAddr("127.0.0.1:8080")
		Expect(err).ToNot(HaveOccurred())
		Expect(reason).To(ContainSubstring("loopback"))
	})

	It("reports a bind address, which is not an address at all", func() {
		reason, err := cluster.CheckAdvertisedAddr("0.0.0.0:8080")
		Expect(err).ToNot(HaveOccurred())
		Expect(reason).To(ContainSubstring("unspecified"))
	})

	It("reports a scoped literal, which net.ParseIP alone would wave through as a name", func() {
		// The zone has to be split off before parsing, or this address is
		// indistinguishable from a hostname and collects no warning at all.
		reason, err := cluster.CheckAdvertisedAddr("[fe80::1%eth0]:8080")
		Expect(err).ToNot(HaveOccurred())
		Expect(reason).ToNot(BeEmpty(), "a scoped address peers cannot dial was accepted in silence")
		Expect(reason).To(ContainSubstring("fe80::1%eth0"),
			"the reported address must carry its zone, or it is not the address being rejected")
	})
})
