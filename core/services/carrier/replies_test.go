package carrier_test

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The request/reply of the frontends (the chat start, the replay of chat
// events, the background jobs of a turn) goes through the same holder as the
// fan-out. A carrier that has no request/reply between processes leaves
// Set.Replies nil, and the holder answers that nothing was delivered, which
// the chat start reads as "run it here" and no caller reads as absence.
var _ = Describe("Broadcaster holder, request and reply", func() {
	var (
		nats, tun *fakeCarrier
		cur       atomic.Pointer[carrier.Set]
		h         *carrier.Broadcaster
	)

	BeforeEach(func() {
		nats = newFakeCarrier(cluster.CarrierNATS, 1)
		nats.set.Replies = nats.bus
		tun = newFakeCarrier(cluster.CarrierTunnel, 2)
		cur = atomic.Pointer[carrier.Set]{}
		cur.Store(nats.set)
		h = carrier.NewBroadcaster(&cur)
	})

	echo := func(prefix string) func([]byte, func([]byte)) {
		return func(data []byte, reply func([]byte)) { reply(append([]byte(prefix), data...)) }
	}

	It("sends a request on the active carrier and returns the reply", func() {
		_, err := nats.bus.SubscribeReply("chats.replay.r1", echo("nats:"))
		Expect(err).ToNot(HaveOccurred())

		out, err := h.Request("chats.replay.r1", []byte("x"), time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("nats:x"))
	})

	It("answers that nothing was delivered, and not that nobody listens, when the active carrier has no request and reply", func() {
		cur.Store(tun.set)

		_, err := h.Request("chats.replay.r1", []byte("x"), time.Second)
		Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
		Expect(messaging.IsNoResponders(err)).To(BeFalse(), "a carrier that cannot carry the request says nothing about who serves it")
		_, err = h.RequestInstance("r1", "chats.replay.r1", []byte("x"), time.Second)
		Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
		_, err = h.RequestQueue("chats.start", []byte("x"), time.Second, nil)
		Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
	})

	It("sends an addressed or a queue request on subject alone to a carrier that routes by subject", func() {
		_, err := nats.bus.SubscribeReply("chats.replay.r1", echo("nats:"))
		Expect(err).ToNot(HaveOccurred())
		out, err := h.RequestInstance("r1", "chats.replay.r1", []byte("x"), time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("nats:x"))
		out, err = h.RequestQueue("chats.replay.r1", []byte("y"), time.Second, func([]byte) bool { return true })
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("nats:y"), "moveOn is not used where the carrier picks the member")
	})

	It("keeps a reply subscription across a change of carrier, and serves it only where the carrier has request and reply", func() {
		cur.Store(tun.set)
		h2 := carrier.NewBroadcaster(&cur)
		sub, err := h2.SubscribeReply("chats.replay.r1", echo("held:"))
		Expect(err).ToNot(HaveOccurred())

		// The tunnel has no request and reply, so nothing is subscribed anywhere.
		_, ok := nats.bus.DeliverReply("chats.replay.r1", []byte("x"))
		Expect(ok).To(BeFalse())

		// Listening on NATS attaches the subscription there.
		Expect(h2.Listen(nats.set)).To(Succeed())
		out, ok := nats.bus.DeliverReply("chats.replay.r1", []byte("x"))
		Expect(ok).To(BeTrue())
		Expect(string(out)).To(Equal("held:x"))

		// Releasing NATS detaches it again.
		Expect(h2.Release(nats.set)).To(Succeed())
		_, ok = nats.bus.DeliverReply("chats.replay.r1", []byte("x"))
		Expect(ok).To(BeFalse())

		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("subscribes a queue reply in its group on the carrier that has request and reply", func() {
		sub, err := h.QueueSubscribeReply("chats.start", "chat-workers", echo("q:"))
		Expect(err).ToNot(HaveOccurred())
		Expect(nats.bus.QueueGroups()).To(HaveKeyWithValue("chats.start", "chat-workers"))

		out, err := h.Request("chats.start", []byte("go"), time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("q:go"))

		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("refuses a reply subscription on a subject that no carrier serves", func() {
		_, err := h.SubscribeReply("nosuchroot.x", echo(""))
		Expect(err).To(HaveOccurred())
	})
})
