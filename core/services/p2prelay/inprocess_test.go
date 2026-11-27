package p2prelay_test

import (
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
)

var _ = Describe("NewInProcess", func() {
	It("relays only between admitted peers", func() {
		server, a, b, stranger := newPeer(), newPeer(), newPeer(), newPeer()
		paired := map[string]bool{a.ID().String(): true, b.ID().String(): true}
		admit := func(p peer.ID) bool { return paired[p.String()] }
		lim := p2prelay.DefaultLimits()
		closer, err := p2prelay.NewInProcess(server, admit, lim)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(closer.Close)
		r := &testRelay{h: server}

		Expect(reserve(a, r)).To(Succeed())
		Expect(reserve(stranger, r)).ToNot(Succeed())
		Expect(circuit(b, a, r)).To(Succeed())
		n, _, err := download(b, a, 64<<10)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(64 << 10))
		Expect(circuit(stranger, a, r)).ToNot(Succeed())
	})
})

var _ = Describe("NewInProcess with a reservation check", func() {
	It("refuses a reservation to a peer the check refuses, and still relays its circuits", func() {
		server, phone, laptop := newPeer(), newPeer(), newPeer()
		paired := map[string]bool{phone.ID().String(): true, laptop.ID().String(): true}
		admit := func(p peer.ID) bool { return paired[p.String()] }
		mayReserve := func(p peer.ID) bool { return p == laptop.ID() }
		closer, err := p2prelay.NewInProcess(server, admit, p2prelay.DefaultLimits(), p2prelay.WithReserveAdmit(mayReserve))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(closer.Close)
		r := &testRelay{h: server}

		Expect(reserve(phone, r)).ToNot(Succeed())
		Expect(reserve(laptop, r)).To(Succeed())
		Expect(circuit(phone, laptop, r)).To(Succeed())
	})
})

var _ = Describe("NewInProcess with a circuit check", func() {
	It("refuses a circuit from a peer the check refuses", func() {
		server, phone, laptop, worker := newPeer(), newPeer(), newPeer(), newPeer()
		paired := map[string]bool{phone.ID().String(): true, laptop.ID().String(): true, worker.ID().String(): true}
		admit := func(p peer.ID) bool { return paired[p.String()] }
		instance := func(p peer.ID) bool { return p != phone.ID() }
		closer, err := p2prelay.NewInProcess(server, admit, p2prelay.DefaultLimits(),
			p2prelay.WithReserveAdmit(instance), p2prelay.WithCircuitAdmit(instance))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(closer.Close)
		r := &testRelay{h: server}

		Expect(reserve(worker, r)).To(Succeed())
		Expect(circuit(phone, worker, r)).ToNot(Succeed(), "a user's phone opens no circuit")
		Expect(circuit(laptop, worker, r)).To(Succeed())
	})
})
