package p2pnet

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Address cache of paired peers", func() {
	addr := func(i int) []ma.Multiaddr {
		return []ma.Multiaddr{ma.StringCast("/ip4/10.0.0.1/tcp/" + strconv.Itoa(1000+i))}
	}
	id := func(i int) peer.ID { return peer.ID("peer-" + strconv.Itoa(i)) }

	It("evicts the least recently used peer first", func() {
		c := newAddrCache()
		for i := range addrCachePeers {
			c.put(id(i), addr(i))
		}
		c.put(id(0), addr(0)) // touch: peer 1 is now the oldest
		c.put(id(addrCachePeers), addr(addrCachePeers))
		got := map[peer.ID]bool{}
		for _, e := range c.snapshot() {
			got[e.peer] = true
		}
		Expect(got).To(HaveLen(addrCachePeers))
		Expect(got).NotTo(HaveKey(id(1)))
		Expect(got).To(HaveKey(id(0)))
	})

	It("never holds more than the bound, however many peers are put", func() {
		c := newAddrCache()
		for i := range 5 * addrCachePeers {
			c.put(id(i), addr(i))
		}
		Expect(c.snapshot()).To(HaveLen(addrCachePeers))
	})

	It("caps the addresses of one peer and replaces them on a new put", func() {
		c := newAddrCache()
		var many []ma.Multiaddr
		for i := range 3 * addrCachePerPeer {
			many = append(many, addr(i)...)
		}
		c.put(id(1), many)
		Expect(c.snapshot()[0].addrs).To(HaveLen(addrCachePerPeer))
		c.put(id(1), addr(7))
		Expect(c.snapshot()).To(HaveLen(1))
		Expect(c.snapshot()[0].addrs).To(Equal(addr(7)))
		c.put(id(1), nil) // nothing learned: the old addresses stay
		Expect(c.snapshot()[0].addrs).To(Equal(addr(7)))
	})

	It("forgets a revoked peer", func() {
		c := newAddrCache()
		c.put(id(1), addr(1))
		c.put(id(2), addr(2))
		c.forget(id(1))
		Expect(c.snapshot()).To(HaveLen(1))
		Expect(c.snapshot()[0].peer).To(Equal(id(2)))
		c.forget(id(1)) // twice is fine
	})

	It("keeps only direct addresses on the IP of a live connection", func() {
		ips := map[string]bool{"192.0.2.7": true, "198.51.100.4": true, "127.0.0.1": true, "169.254.1.1": true}
		in := []ma.Multiaddr{
			ma.StringCast("/ip4/192.0.2.7/tcp/4001"),                // kept
			ma.StringCast("/ip4/192.0.2.7/udp/4001/quic-v1"),        // kept
			ma.StringCast("/ip4/203.0.113.5/tcp/4001"),              // not a connection IP
			ma.StringCast("/ip4/127.0.0.1/tcp/4001"),                // loopback
			ma.StringCast("/ip4/169.254.1.1/tcp/4001"),              // link-local
			ma.StringCast("/ip4/198.51.100.4/tcp/4001/p2p-circuit"), // relay
			ma.StringCast("/dns4/example.net/tcp/4001"),             // no IP
		}
		got := directAddrsOnIPs(in, ips)
		Expect(got).To(Equal(in[:2]))
	})
})

var _ = Describe("Redial of paired peers", func() {
	newRedialHost := func(g *Allowlist) *Host {
		GinkgoHelper()
		h, err := New(context.Background(), Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
			Gate:         g,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(h.Close)
		return h
	}

	// pairedPair returns a server that holds the client as a paired peer and a
	// client that admits the server at connection level only, as a client of
	// a remote does.
	pairedPair := func() (server, client *Host, sg, cg *Allowlist) {
		GinkgoHelper()
		sg, cg = NewAllowlist(), NewAllowlist()
		server, client = newRedialHost(sg), newRedialHost(cg)
		sg.Allow(client.ID())
		cg.AllowConn(server.ID())
		return
	}

	// addrsOf returns an interface address source that lists ips.
	addrsOf := func(ips ...string) func() ([]net.Addr, error) {
		return func() ([]net.Addr, error) {
			out := make([]net.Addr, 0, len(ips))
			for _, ip := range ips {
				out = append(out, &net.IPNet{IP: net.ParseIP(ip), Mask: net.CIDRMask(24, 32)})
			}
			return out, nil
		}
	}

	// connectPaired connects server to client through the redialer and
	// returns the direct connections between them.
	connectPaired := func(server, client *Host) []network.Conn {
		GinkgoHelper()
		server.redial.cache.put(client.ID(), client.h.Addrs())
		server.Redial(client.ID())
		Eventually(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "10s", "50ms").Should(Equal(network.Connected))
		conns := server.h.Network().ConnsToPeer(client.ID())
		Expect(conns).ToNot(BeEmpty())
		return conns
	}

	It("dials a paired peer at the cached address when it is not connected", func() {
		server, client, _, _ := pairedPair()
		server.redial.cache.put(client.ID(), client.h.Addrs())
		Expect(server.h.Network().Connectedness(client.ID())).ToNot(Equal(network.Connected))

		server.Redial(client.ID())

		Eventually(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "10s", "50ms").Should(Equal(network.Connected))
	})

	It("does not dial a peer that is not paired", func() {
		server, client, sg, _ := pairedPair()
		sg.Disallow(client.ID())
		server.redial.cache.put(client.ID(), client.h.Addrs())
		server.Redial(client.ID())
		Consistently(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "500ms", "50ms").ShouldNot(Equal(network.Connected))
	})

	It("does not admit the server at stream level, and does not open the pairing window", func() {
		_, _, _, cg := pairedPair()
		server := peer.ID("server")
		cg.AllowConn(server)
		Expect(cg.AdmitConn(server)).To(BeTrue())
		Expect(cg.AdmitStream(server, ProtoHTTP)).To(BeFalse())
		Expect(cg.Allowed(server)).To(BeFalse())
		cg.DisallowConn(server)
		Expect(cg.AdmitConn(server)).To(BeFalse())
	})

	It("purges the cache entry and closes the connections when a peer is revoked", func() {
		server, client, _, _ := pairedPair()
		server.redial.cache.put(client.ID(), client.h.Addrs())
		server.Redial(client.ID())
		Eventually(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "10s", "50ms").Should(Equal(network.Connected))

		server.Revoke(client.ID())

		Expect(server.redial.cache.snapshot()).To(BeEmpty())
		Eventually(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "5s", "50ms").ShouldNot(Equal(network.Connected))
		server.Redial(client.ID()) // not paired any more: no dial
		Consistently(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "500ms", "50ms").ShouldNot(Equal(network.Connected))
	})

	It("redials every cached peer when the addresses change", func() {
		server, client, _, _ := pairedPair()
		server.redial.cache.put(client.ID(), client.h.Addrs())
		server.redial.addressesChanged()
		Eventually(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "10s", "50ms").Should(Equal(network.Connected))
	})
	It("keeps the direct connections and still redials when the interface addresses cannot be read", func() {
		server, connected, sg, _ := pairedPair()
		conns := connectPaired(server, connected)

		// A second paired peer that is cached but not connected.
		cg2 := NewAllowlist()
		waiting := newRedialHost(cg2)
		sg.Allow(waiting.ID())
		cg2.AllowConn(server.ID())
		server.redial.cache.put(waiting.ID(), waiting.h.Addrs())
		Expect(server.h.Network().Connectedness(waiting.ID())).ToNot(Equal(network.Connected))

		server.redial.interfaceAddrs = func() ([]net.Addr, error) {
			return nil, errors.New("route ip+net: netlinkrib: permission denied")
		}

		server.redial.addressesChanged()

		Eventually(func() network.Connectedness { return server.h.Network().Connectedness(waiting.ID()) }, "10s", "50ms").Should(Equal(network.Connected))
		Consistently(func() bool {
			for _, c := range conns {
				if c.IsClosed() {
					return true
				}
			}
			return false
		}, "500ms", "50ms").Should(BeFalse(), "a failed lookup is not a lost address")
		Expect(server.h.Network().Connectedness(connected.ID())).To(Equal(network.Connected))
	})

	// The test hosts listen on loopback, which localIPs never reports, so
	// their connections count as using an address the host no longer has.
	DescribeTable("closes the direct connections on an address this host no longer has, then redials",
		func(source func() ([]net.Addr, error)) {
			server, client, _, _ := pairedPair()
			conns := connectPaired(server, client)
			server.redial.interfaceAddrs = source

			server.redial.addressesChanged()

			for _, c := range conns {
				Eventually(c.IsClosed, "5s", "50ms").Should(BeTrue())
			}
			Eventually(func() network.Connectedness { return server.h.Network().Connectedness(client.ID()) }, "10s", "50ms").Should(Equal(network.Connected))
		},
		Entry("an address list without the connection's IP", addrsOf("192.0.2.1")),
		Entry("an empty address list", addrsOf()),
	)
})

var _ = Describe("Local IPs of the redialer", func() {
	It("keeps the non-loopback addresses of the interfaces", func() {
		r := &redialer{interfaceAddrs: func() ([]net.Addr, error) {
			return []net.Addr{
				&net.IPNet{IP: net.ParseIP("192.0.2.7"), Mask: net.CIDRMask(24, 32)},
				&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
				&net.IPAddr{IP: net.ParseIP("192.0.2.9")}, // not an *net.IPNet: ignored
			}, nil
		}}
		ips, err := r.localIPs()
		Expect(err).ToNot(HaveOccurred())
		Expect(ips).To(Equal(map[string]bool{"192.0.2.7": true}))
	})

	It("returns the error of the lookup, not an empty set", func() {
		lookupErr := errors.New("netlinkrib: permission denied")
		r := &redialer{interfaceAddrs: func() ([]net.Addr, error) { return nil, lookupErr }}
		ips, err := r.localIPs()
		Expect(err).To(MatchError(lookupErr))
		Expect(ips).To(BeNil())
	})
})
