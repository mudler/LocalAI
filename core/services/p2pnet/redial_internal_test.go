package p2pnet

import (
	"context"
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
})
