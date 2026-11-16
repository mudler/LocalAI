package carrier_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// peerNet stands for the peer links: a request reaches the handlers of the
// replica it names, unless a spec makes that replica fail first.
type peerNet struct {
	mu       sync.Mutex
	replicas map[string]*tunnel.Replies
	fail     map[string]error
	hang     map[string]bool
	asked    []string
	shares   []time.Duration
}

func newPeerNet() *peerNet {
	return &peerNet{replicas: map[string]*tunnel.Replies{}, fail: map[string]error{}, hang: map[string]bool{}}
}

func (n *peerNet) replica(id string) *tunnel.Replies {
	r := tunnel.NewReplies(id, messaging.MaxBroadcastBytes)
	n.mu.Lock()
	n.replicas[id] = r
	n.mu.Unlock()
	return r
}

func (n *peerNet) failWith(id string, err error) {
	n.mu.Lock()
	n.fail[id] = err
	n.mu.Unlock()
}

func (n *peerNet) askedOf() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.asked)
}

func (n *peerNet) Request(ctx context.Context, target, subject string, data []byte, _ int, openWithin time.Duration) ([]byte, error) {
	n.mu.Lock()
	n.asked = append(n.asked, target)
	n.shares = append(n.shares, openWithin)
	err, r, hang := n.fail[target], n.replicas[target], n.hang[target]
	n.mu.Unlock()
	if hang {
		// A replica in the liveness window whose dial does not complete: the open
		// waits for its share of the budget, as tunnel.PeerPool.Open does.
		octx := ctx
		if openWithin > 0 {
			var cancel context.CancelFunc
			octx, cancel = context.WithTimeout(ctx, openWithin)
			defer cancel()
		}
		<-octx.Done()
		return nil, fmt.Errorf("%w: %w", tunnel.ErrRequestNotSent, octx.Err())
	}
	switch {
	case err != nil:
		return nil, err
	case r == nil:
		return nil, fmt.Errorf("%w: %w", tunnel.ErrRequestNotSent, cluster.ErrInstanceNotFound)
	}
	return r.Dispatch(ctx, subject, data)
}

var (
	unreachable = fmt.Errorf("%w: %w", tunnel.ErrRequestNotSent, tunnel.ErrPeerUnreachable)
	replyLost   = fmt.Errorf("%w: link closed", tunnel.ErrReplyLost)
)

var _ = Describe("Request and reply on the tunnel carrier", func() {
	var (
		peers   *peerNet
		local   *tunnel.Replies
		members []string
		rr      messaging.RequestReply
	)

	BeforeEach(func() {
		peers = newPeerNet()
		local = tunnel.NewReplies("self", messaging.MaxBroadcastBytes)
		members = nil
		rr = carrier.NewTunnelReplies("self", peers, local, map[string]carrier.QueueMembers{
			messaging.SubjectChatStart: func(context.Context) ([]string, error) { return slices.Clone(members), nil },
		})
	})

	serve := func(id, subject, prefix string) {
		peers.replica(id).Handle(subject, func(_ context.Context, data []byte) ([]byte, error) {
			return append([]byte(prefix), data...), nil
		})
	}
	addressed := func(instance, subject string) ([]byte, error) {
		return rr.(messaging.InstanceRequester).RequestInstance(instance, subject, []byte("x"), time.Second)
	}
	queued := func(moveOn func([]byte) bool) ([]byte, error) {
		return rr.(messaging.QueueRequester).RequestQueue(messaging.SubjectChatStart, []byte("x"), time.Second, moveOn)
	}

	Describe("a request to one replica", func() {
		It("reaches the handler of the replica that it names", func() {
			serve("r2", "chats.replay.r2", "r2:")
			out, err := addressed("r2", "chats.replay.r2")
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(Equal("r2:x"))
		})

		It("is served in this process when it names this replica", func() {
			sub, err := rr.SubscribeReply("chats.replay.self", func(data []byte, reply func([]byte)) { reply(append([]byte("me:"), data...)) })
			Expect(err).ToNot(HaveOccurred())
			out, err := addressed("self", "chats.replay.self")
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(Equal("me:x"))
			Expect(peers.askedOf()).To(BeEmpty())
			Expect(sub.Unsubscribe()).To(Succeed())
			_, err = addressed("self", "chats.replay.self")
			Expect(messaging.IsNoResponders(err)).To(BeFalse(), "a named replica is never read as absent")
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
		})

		DescribeTable("classifies a failure",
			func(setup func(), noResponders, notDelivered bool) {
				setup()
				_, err := addressed("r2", "chats.jobs.h")
				Expect(err).To(HaveOccurred())
				Expect(messaging.IsNoResponders(err)).To(Equal(noResponders), "no responders: %v", err)
				Expect(errors.Is(err, messaging.ErrNotDelivered)).To(Equal(notDelivered), "not delivered: %v", err)
			},
			// A live replica whose heartbeats stalled past the reap window has no
			// row, and one that did not prepare a forced change of carrier serves
			// nothing yet. Neither is a process that left.
			Entry("a replica that is not part of the deployment: not delivered, not absence", func() {}, false, true),
			Entry("a replica that serves no handler for the subject: not delivered, not absence", func() { peers.replica("r2") }, false, true),
			Entry("a peer that does not answer: not delivered, not absence", func() { peers.failWith("r2", unreachable) }, false, true),
			Entry("a release that predates request and reply: not delivered, not absence", func() {
				peers.failWith("r2", fmt.Errorf("%w: relay-bad-request", tunnel.ErrRequestUnsupported))
			}, false, true),
			Entry("a replica that is not the one named: not delivered, not absence", func() {
				peers.failWith("r2", fmt.Errorf("%w: this replica is r9", tunnel.ErrNotOwner))
			}, false, true),
			Entry("a reply that was lost after the request was sent: unknown", func() { peers.failWith("r2", replyLost) }, false, false),
			Entry("a handler that failed: unknown", func() {
				peers.failWith("r2", fmt.Errorf("%w: store down", tunnel.ErrHandlerFailed))
			}, false, false),
		)

		It("refuses a request above the bound before it sends anything", func() {
			_, err := rr.(messaging.InstanceRequester).RequestInstance("r2", "chats.jobs.h", make([]byte, messaging.MaxBroadcastBytes+1), time.Second)
			Expect(errors.Is(err, messaging.ErrPayloadTooLarge)).To(BeTrue())
			Expect(peers.askedOf()).To(BeEmpty())
		})

		It("reports a reply above the bound as a handler that ran, on this replica and on another", func() {
			_, err := rr.SubscribeReply("chats.replay.self", func(_ []byte, reply func([]byte)) {
				reply(make([]byte, messaging.MaxBroadcastBytes+1))
			})
			Expect(err).ToNot(HaveOccurred())
			peers.failWith("r2", fmt.Errorf("%w: %w: from afar", tunnel.ErrHandlerFailed, tunnel.ErrReplyTooLarge))
			for _, instance := range []string{"self", "r2"} {
				_, err := addressed(instance, "chats.replay."+instance)
				Expect(errors.Is(err, tunnel.ErrReplyTooLarge)).To(BeTrue(), "%s: %v", instance, err)
				Expect(errors.Is(err, messaging.ErrPayloadTooLarge)).To(BeFalse(), "%s: the request was sent and ran", instance)
				Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeFalse(), instance)
				Expect(messaging.IsNoResponders(err)).To(BeFalse(), instance)
			}
		})

		It("reads a replica that serves too many requests as not delivered", func() {
			peers.failWith("r2", fmt.Errorf("%w: busy", tunnel.ErrReplicaBusy))
			_, err := addressed("r2", "chats.jobs.h")
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue(), "got %v", err)
			Expect(messaging.IsNoResponders(err)).To(BeFalse())
		})

		It("sends nothing for a subject that names no replica and no queue", func() {
			_, err := rr.Request("chats.replay.r2", []byte("x"), time.Second)
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
			Expect(messaging.IsNoResponders(err)).To(BeFalse())
		})

		It("reports a handler that sent no reply as a failure whose outcome is unknown", func() {
			_, err := rr.SubscribeReply("chats.replay.self", func([]byte, func([]byte)) {})
			Expect(err).ToNot(HaveOccurred())
			_, err = addressed("self", "chats.replay.self")
			Expect(errors.Is(err, tunnel.ErrHandlerFailed)).To(BeTrue(), "got %v", err)
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeFalse())
		})
	})

	Describe("a request to a queue", func() {
		It("answers that nobody listens when no replica serves the queue", func() {
			_, err := queued(nil)
			Expect(messaging.IsNoResponders(err)).To(BeTrue())
		})

		It("answers that nothing was delivered when the members cannot be listed", func() {
			rr = carrier.NewTunnelReplies("self", peers, local, map[string]carrier.QueueMembers{
				messaging.SubjectChatStart: func(context.Context) ([]string, error) { return nil, errors.New("database down") },
			})
			_, err := queued(nil)
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
		})

		It("moves to another member only when the first proves that it ran nothing", func() {
			members = []string{"w1", "w2", "w3"}
			serve("w1", messaging.SubjectChatStart, "w1:")
			peers.failWith("w2", unreachable)
			peers.replica("w3") // serves nothing
			for range 10 {
				out, err := queued(nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(out)).To(Equal("w1:x"))
			}
		})

		It("returns a lost reply without a retry, so that a start never runs twice", func() {
			members = []string{"w1", "w2", "w3"}
			for _, id := range members {
				peers.failWith(id, replyLost)
			}
			_, err := queued(nil)
			Expect(errors.Is(err, tunnel.ErrReplyLost)).To(BeTrue())
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeFalse())
			Expect(peers.askedOf()).To(HaveLen(1))
		})

		It("moves to another member on a refusal that moveOn reads as proof, and returns the last one when all refuse", func() {
			members = []string{"w1", "w2"}
			serve("w1", messaging.SubjectChatStart, "full")
			serve("w2", messaging.SubjectChatStart, "ok")
			isFull := func(b []byte) bool { return string(b[:4]) == "full" }
			for range 10 {
				out, err := queued(isFull)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(out)).To(Equal("okx"))
			}
			serve("w2", messaging.SubjectChatStart, "full")
			out, err := queued(isFull)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(Equal("fullx"))
		})

		It("answers that nobody listens only when every member said so", func() {
			members = []string{"w1", "w2"}
			peers.replica("w1")
			peers.replica("w2")
			_, err := queued(nil)
			Expect(messaging.IsNoResponders(err)).To(BeTrue())

			peers.failWith("w2", unreachable)
			_, err = queued(nil)
			Expect(messaging.IsNoResponders(err)).To(BeFalse(), "an unreachable member is not absence")
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
		})

		It("gives each member a share of the budget to open its stream, so a member that does not answer leaves time for the others", func() {
			members = []string{"w1", "w2"}
			peers.mu.Lock()
			peers.hang["w1"] = true
			peers.mu.Unlock()
			serve("w2", messaging.SubjectChatStart, "w2:")
			for range 4 {
				began := time.Now()
				out, err := queued(nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(out)).To(Equal("w2:x"))
				Expect(time.Since(began)).To(BeNumerically("<", time.Second))
			}
			peers.mu.Lock()
			defer peers.mu.Unlock()
			for _, share := range peers.shares {
				Expect(share).To(BeNumerically(">", 0))
				Expect(share).To(BeNumerically("<=", time.Second))
			}
		})

		It("tries at most three members", func() {
			members = []string{"w1", "w2", "w3", "w4", "w5"}
			for _, id := range members {
				peers.failWith(id, unreachable)
			}
			_, err := queued(nil)
			Expect(errors.Is(err, messaging.ErrNotDelivered)).To(BeTrue())
			Expect(peers.askedOf()).To(HaveLen(3))
		})
	})

	Describe("through the holder, across a change of carrier", func() {
		It("follows the subscriptions of the holder, and ends a request on the carrier where it began", func() {
			nats := newFakeCarrier(cluster.CarrierNATS, 1)
			nats.set.Replies = nats.bus
			tun := newFakeCarrier(cluster.CarrierTunnel, 2)
			// The replica that answers is this one: its handler is reached through
			// the same registry as a peer's would be.
			tun.set.Replies = rr
			var cur atomic.Pointer[carrier.Set]
			cur.Store(nats.set)
			h := carrier.NewBroadcaster(&cur)

			release := make(chan struct{})
			var calls atomic.Int32
			sub, err := h.SubscribeReply("chats.replay.self", func(data []byte, reply func([]byte)) {
				if calls.Add(1) == 2 {
					<-release // the second request is in flight across the change
				}
				reply(append([]byte("ok:"), data...))
			})
			Expect(err).ToNot(HaveOccurred())

			out, err := h.RequestInstance("self", "chats.replay.self", []byte("1"), time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(Equal("ok:1"))

			// NATS to the tunnel.
			Expect(h.Listen(tun.set)).To(Succeed())
			cur.Store(tun.set)
			inflight := make(chan []byte, 1)
			go func() {
				defer GinkgoRecover()
				out, err := h.RequestInstance("self", "chats.replay.self", []byte("2"), 5*time.Second)
				Expect(err).ToNot(HaveOccurred())
				inflight <- out
			}()
			Eventually(calls.Load).Should(BeEquivalentTo(2))

			// And back, while the request on the tunnel runs.
			Expect(h.Listen(nats.set)).To(Succeed())
			cur.Store(nats.set)
			Expect(h.Release(tun.set)).To(Succeed())
			out, err = h.RequestInstance("self", "chats.replay.self", []byte("3"), time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(Equal("ok:3"), "a new request goes to NATS")

			close(release)
			Eventually(inflight).Should(Receive(Equal([]byte("ok:2"))), "the request in flight ends on the tunnel")
			_, err = local.Dispatch(context.Background(), "chats.replay.self", nil)
			Expect(errors.Is(err, tunnel.ErrNoResponder)).To(BeTrue(), "the released tunnel serves the subject no more")
			Expect(sub.Unsubscribe()).To(Succeed())
		})
	})
})
