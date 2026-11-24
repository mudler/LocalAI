package p2prelaymgr_test

import (
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
)

// admissionOf returns the admission of peer on the relay, or a zero value.
func admissionOf(m p2prelaymgr.Manager, relayID, peer string) p2prelaymgr.Admission {
	for _, a := range m.NodeAdmission(peer) {
		if a.RelayID == relayID {
			return a
		}
	}
	return p2prelaymgr.Admission{}
}

var _ = Describe("Node peers", func() {
	var (
		srv   *server
		alice string
		bob   string
	)
	BeforeEach(func() {
		srv = newServer()
		alice, bob = randomPeer(), randomPeer()
		srv.setPaired(alice, bob)
	})

	It("admits a node peer at the next sync, and does not write the set again while nothing changes", func() {
		relay := newTestRelay(gatedCfg(srv.host.ID()))
		m := srv.manager(nil, false)
		_, err := m.Add(ctx10(), relay.addr(), "home", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(srv.members(relay.id())).To(ConsistOf(alice, bob))

		node := randomPeer()
		Expect(admissionOf(m, relay.id(), node).State).To(BeEmpty(), "a peer the server does not vouch for has no admission")
		srv.setNodes(node)
		m.Resync()
		Eventually(func() []string { return srv.members(relay.id()) }, 10*time.Second).Should(ConsistOf(alice, bob, node))
		Eventually(func() string { return admissionOf(m, relay.id(), node).State }).Should(Equal(p2prelaymgr.AdmissionAdmitted))
		Expect(admissionOf(m, relay.id(), node).Label).To(Equal("home"))
		Expect(m.List()[0].State).To(Equal(p2prelaymgr.StateInSync))

		seq := srv.seq(relay.id())
		for range 3 {
			m.Resync()
		}
		Consistently(func() int64 { return srv.seq(relay.id()) }, 1500*time.Millisecond, 250*time.Millisecond).Should(Equal(seq),
			"a repeated sync of the same set sends no write")
		Expect(admissionOf(m, relay.id(), node).State).To(Equal(p2prelaymgr.AdmissionAdmitted))
	})

	It("removes a node peer that the server no longer vouches for", func() {
		relay := newTestRelay(gatedCfg(srv.host.ID()))
		node := randomPeer()
		srv.setNodes(node)
		m := srv.manager(nil, false)
		_, err := m.Add(ctx10(), relay.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(srv.members(relay.id())).To(ConsistOf(alice, bob, node))

		srv.setNodes()
		m.Resync()
		Eventually(func() []string { return srv.members(relay.id()) }, 10*time.Second).Should(ConsistOf(alice, bob))
		Expect(m.NodeAdmission(node)).To(BeEmpty())
	})

	It("does not list a paired device twice when it is also a node peer", func() {
		relay := newTestRelay(gatedCfg(srv.host.ID()))
		srv.setNodes(alice)
		m := srv.manager(nil, false)
		r, err := m.Add(ctx10(), relay.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(r.State).To(Equal(p2prelaymgr.StateInSync))
		Expect(srv.members(relay.id())).To(ConsistOf(alice, bob))
	})

	It("keeps the paired devices and refuses the node peers past the relay's limit per admin", func() {
		cfg := gatedCfg(srv.host.ID())
		cfg.MaxPeersPerAdmin = 3
		relay := newTestRelay(cfg)
		n1, n2 := randomPeer(), randomPeer()
		srv.setNodes(n1, n2)
		m := srv.manager(nil, false)
		r, err := m.Add(ctx10(), relay.addr(), "home", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(r.State).To(Equal(p2prelaymgr.StateInSync), "the paired set and the nodes that fit are in sync")

		// The nodes are taken in the order of their peer IDs, so that every
		// server of a cluster admits the same one.
		first, second := min(n1, n2), max(n1, n2)
		Expect(srv.members(relay.id())).To(ConsistOf(alice, bob, first))
		Expect(admissionOf(m, relay.id(), first).State).To(Equal(p2prelaymgr.AdmissionAdmitted))
		refused := admissionOf(m, relay.id(), second)
		Expect(refused.State).To(Equal(p2prelaymgr.AdmissionRefused))
		Expect(refused.Reason).To(ContainSubstring("3"), "the reason names the limit")

		// The next sync keeps the same choice and writes nothing.
		seq := srv.seq(relay.id())
		m.Resync()
		Consistently(func() int64 { return srv.seq(relay.id()) }, time.Second, 250*time.Millisecond).Should(Equal(seq))
	})

	It("makes room for a new paired device by dropping a node peer", func() {
		cfg := gatedCfg(srv.host.ID())
		cfg.MaxPeersPerAdmin = 3
		relay := newTestRelay(cfg)
		srv.setPaired(alice)
		n1, n2 := randomPeer(), randomPeer()
		srv.setNodes(n1, n2)
		m := srv.manager(nil, true)
		_, err := m.Add(ctx10(), relay.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(srv.members(relay.id())).To(ConsistOf(alice, n1, n2))
		Expect(m.Start(ctx10())).To(Succeed())

		carol := randomPeer()
		srv.setPaired(alice, carol)
		m.PeerAdded(carol)
		Eventually(func() []string { return srv.members(relay.id()) }, 15*time.Second).Should(ConsistOf(alice, carol, min(n1, n2)))
		Eventually(func() string { return admissionOf(m, relay.id(), max(n1, n2)).State }).Should(Equal(p2prelaymgr.AdmissionRefused))
	})

	It("refuses every node peer when the paired devices fill the relay", func() {
		cfg := gatedCfg(srv.host.ID())
		cfg.MaxPeersPerAdmin = 2
		relay := newTestRelay(cfg)
		node := randomPeer()
		srv.setNodes(node)
		m := srv.manager(nil, false)
		_, err := m.Add(ctx10(), relay.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(srv.members(relay.id())).To(ConsistOf(alice, bob))
		Expect(admissionOf(m, relay.id(), node).State).To(Equal(p2prelaymgr.AdmissionRefused))
	})

	It("skips a relay that this server does not administer, and waits for one it cannot reach", func() {
		admin := newServer()
		other := newTestRelay(gatedCfg(admin.host.ID()))
		admin.setPaired(srv.host.ID().String())
		_, err := admin.manager(nil, false).Add(ctx10(), other.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		gone := newTestRelay(gatedCfg(srv.host.ID()))
		goneAddr, goneID := gone.addr(), gone.id()
		gone.stop()

		node := randomPeer()
		srv.setNodes(node)
		m := srv.manager(nil, false)
		_, err = m.Add(ctx10(), other.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		_, err = m.Add(ctx10(), goneAddr, "", "")
		Expect(err).ToNot(HaveOccurred())

		Expect(admissionOf(m, other.id(), node).State).To(Equal(p2prelaymgr.AdmissionSkipped))
		Expect(admissionOf(m, other.id(), node).Reason).ToNot(BeEmpty())
		Expect(admissionOf(m, goneID, node).State).To(Equal(p2prelaymgr.AdmissionPending))
		Expect(admissionOf(m, goneID, node).Reason).ToNot(BeEmpty())
	})

	It("lets two servers of a cluster vouch for the same node on a relay they share, without rewriting each other", func() {
		srv2 := newServer()
		srv2.setPaired(alice, bob)
		relay := newTestRelay(gatedCfg(srv.host.ID(), srv2.host.ID()))
		node := randomPeer()
		srv.setNodes(node)
		srv2.setNodes(node)
		m1, m2 := srv.manager(nil, false), srv2.manager(nil, false)
		_, err := m1.Add(ctx10(), relay.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		_, err = m2.Add(ctx10(), relay.addr(), "", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(srv.members(relay.id())).To(ConsistOf(alice, bob, node))
		Expect(srv2.members(relay.id())).To(ConsistOf(alice, bob, node))

		s1, s2 := srv.seq(relay.id()), srv2.seq(relay.id())
		for range 2 {
			m1.Resync()
			m2.Resync()
		}
		Consistently(func() []int64 { return []int64{srv.seq(relay.id()), srv2.seq(relay.id())} }, time.Second, 250*time.Millisecond).
			Should(Equal([]int64{s1, s2}))

		// The node is deleted: each server drops it from its own set.
		srv.setNodes()
		srv2.setNodes()
		m1.Resync()
		m2.Resync()
		Eventually(func() bool {
			return !slices.Contains(srv.members(relay.id()), node) && !slices.Contains(srv2.members(relay.id()), node)
		}, 10*time.Second).Should(BeTrue())
	})
})
