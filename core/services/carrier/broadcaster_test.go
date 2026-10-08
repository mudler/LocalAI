package carrier_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// received collects the payloads a handler saw.
type received struct {
	mu   sync.Mutex
	data []string
}

func (r *received) handler() func([]byte) {
	return func(b []byte) {
		r.mu.Lock()
		r.data = append(r.data, string(b))
		r.mu.Unlock()
	}
}

func (r *received) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.data...)
}

var _ = Describe("Broadcaster holder", func() {
	var (
		a, b *fakeCarrier
		cur  atomic.Pointer[carrier.Set]
		h    *carrier.Broadcaster
	)

	BeforeEach(func() {
		a = newFakeCarrier(cluster.CarrierNATS, 1)
		b = newFakeCarrier(cluster.CarrierTunnel, 2)
		cur = atomic.Pointer[carrier.Set]{}
		cur.Store(a.set)
		h = carrier.NewBroadcaster(&cur)
	})

	It("publishes on the carrier the pointer names", func() {
		got := &received{}
		_, err := a.bus.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		_, err = b.bus.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())

		Expect(h.Publish("jobs.x", "one")).To(Succeed())
		Expect(got.all()).To(HaveLen(1))
		Expect(a.bus.PublishCount("jobs.x")).To(Equal(1))
		Expect(b.bus.PublishCount("jobs.x")).To(Equal(0))

		cur.Store(b.set)
		Expect(h.Publish("jobs.x", "two")).To(Succeed())
		Expect(a.bus.PublishCount("jobs.x")).To(Equal(1))
		Expect(b.bus.PublishCount("jobs.x")).To(Equal(1))
	})

	It("delivers a subscription that was made before and after a swap", func() {
		got := &received{}
		_, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())

		Expect(h.Listen(b.set)).To(Succeed())
		cur.Store(b.set)

		// A peer that has not flipped yet still publishes on the old carrier.
		Expect(a.bus.Publish("jobs.x", "from-old")).To(Succeed())
		Expect(h.Publish("jobs.x", "from-new")).To(Succeed())

		Expect(got.all()).To(ConsistOf(`"from-old"`, `"from-new"`))
	})

	It("listens on both carriers while a target is attached, and publishes on one", func() {
		got := &received{}
		_, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		Expect(h.Listen(b.set)).To(Succeed())

		Expect(a.bus.Publish("jobs.x", "a")).To(Succeed())
		Expect(b.bus.Publish("jobs.x", "b")).To(Succeed())
		Expect(got.all()).To(ConsistOf(`"a"`, `"b"`))

		Expect(h.Publish("jobs.x", "c")).To(Succeed())
		Expect(a.bus.PublishCount("jobs.x")).To(Equal(2), "the active carrier carries the publish")
		Expect(b.bus.PublishCount("jobs.x")).To(Equal(1), "the target carrier carries none")
	})

	It("subscribes on every attached carrier when a subscription is made during the window", func() {
		Expect(h.Listen(b.set)).To(Succeed())
		got := &received{}
		_, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())

		Expect(a.bus.Publish("jobs.x", "a")).To(Succeed())
		Expect(b.bus.Publish("jobs.x", "b")).To(Succeed())
		Expect(got.all()).To(ConsistOf(`"a"`, `"b"`))
	})

	It("removes a subscription from every carrier when it is unsubscribed", func() {
		got := &received{}
		sub, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		Expect(h.Listen(b.set)).To(Succeed())

		Expect(sub.Unsubscribe()).To(Succeed())
		Expect(a.bus.Publish("jobs.x", "a")).To(Succeed())
		Expect(b.bus.Publish("jobs.x", "b")).To(Succeed())
		Expect(got.all()).To(BeEmpty())

		// It stays gone for a carrier attached later.
		c := newFakeCarrier(cluster.CarrierTunnel, 3)
		Expect(h.Listen(c.set)).To(Succeed())
		Expect(c.bus.Publish("jobs.x", "c")).To(Succeed())
		Expect(got.all()).To(BeEmpty())
	})

	It("tolerates a second Unsubscribe", func() {
		sub, err := h.Subscribe("jobs.x", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub.Unsubscribe()).To(Succeed())
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("drops the old carrier's subscriptions on Release and keeps the new ones", func() {
		got := &received{}
		_, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		Expect(h.Listen(b.set)).To(Succeed())
		cur.Store(b.set)

		Expect(h.Release(a.set)).To(Succeed())
		Expect(a.bus.Publish("jobs.x", "late-old")).To(Succeed())
		Expect(b.bus.Publish("jobs.x", "new")).To(Succeed())
		Expect(got.all()).To(Equal([]string{`"new"`}))
	})

	It("refuses to release the carrier that is active", func() {
		Expect(h.Release(a.set)).To(MatchError(carrier.ErrActiveSet))
	})

	It("treats a second Listen of the same carrier as nothing to do", func() {
		got := &received{}
		_, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		Expect(h.Listen(b.set)).To(Succeed())
		Expect(h.Listen(b.set)).To(Succeed())

		Expect(b.bus.Publish("jobs.x", "b")).To(Succeed())
		Expect(got.all()).To(HaveLen(1), "one subscription per carrier, not two")
	})

	It("leaves nothing behind when a carrier cannot take a subscription", func() {
		got := &received{}
		_, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).ToNot(HaveOccurred())

		broken := newFakeCarrier(cluster.CarrierTunnel, 3)
		boom := errors.New("subscribe refused")
		broken.set.Broadcaster = failingBroadcaster{Broadcaster: broken.bus, err: boom}

		err = h.Listen(broken.set)
		Expect(err).To(MatchError(boom))

		// The target is not attached, and a later subscription does not try it.
		_, err = h.Subscribe("jobs.y", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		Expect(h.Release(broken.set)).To(Succeed(), "releasing an unattached carrier is harmless")
	})

	It("fails a subscription when an attached carrier refuses it, and undoes the part that worked", func() {
		refusing := newFakeCarrier(cluster.CarrierTunnel, 3)
		boom := errors.New("subscribe refused")
		refusing.set.Broadcaster = failingBroadcaster{Broadcaster: refusing.bus, err: boom}
		Expect(h.Listen(refusing.set)).To(Succeed(), "nothing is registered yet, so nothing is refused")

		got := &received{}
		_, err := h.Subscribe("jobs.x", got.handler())
		Expect(err).To(MatchError(boom))

		Expect(a.bus.Publish("jobs.x", "a")).To(Succeed())
		Expect(got.all()).To(BeEmpty(), "the half-made subscription was removed")
	})

	Describe("reconnect hooks", func() {
		It("runs a hook when the active carrier reconnects", func() {
			var n atomic.Int32
			h.OnReconnect(func() { n.Add(1) })
			a.bus.TriggerReconnect()
			Expect(n.Load()).To(Equal(int32(1)))
		})

		It("follows the hook to a carrier attached later", func() {
			var n atomic.Int32
			h.OnReconnect(func() { n.Add(1) })
			Expect(h.Listen(b.set)).To(Succeed())
			cur.Store(b.set)
			b.bus.TriggerReconnect()
			Expect(n.Load()).To(Equal(int32(1)))
		})

		It("runs a hook once per reconnect however often a carrier is attached", func() {
			var n atomic.Int32
			h.OnReconnect(func() { n.Add(1) })
			Expect(h.Listen(b.set)).To(Succeed())
			Expect(h.Release(b.set)).To(Succeed())
			Expect(h.Listen(b.set)).To(Succeed())
			b.bus.TriggerReconnect()
			Expect(n.Load()).To(Equal(int32(1)))
		})

		It("ignores a nil hook", func() {
			h.OnReconnect(nil)
			a.bus.TriggerReconnect()
		})

		It("lets the swap code run every hook once", func() {
			var n atomic.Int32
			h.OnReconnect(func() { n.Add(1) })
			h.OnReconnect(func() { n.Add(1) })
			h.NotifyReconnect()
			Expect(n.Load()).To(Equal(int32(2)))
		})

		It("works with a carrier that has no reconnect notion", func() {
			c := newFakeCarrier(cluster.CarrierTunnel, 3)
			c.set.OnReconnect = nil
			Expect(h.Listen(c.set)).To(Succeed())
		})
	})

	Describe("while a swap is in progress", func() {
		It("does not hold a publish behind a listen that is waiting on a carrier", func() {
			slow := newFakeCarrier(cluster.CarrierTunnel, 3)
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			slow.set.Broadcaster = blockingBroadcaster{Broadcaster: slow.bus, gate: gate, entered: entered}

			_, err := h.Subscribe("jobs.x", func([]byte) {})
			Expect(err).ToNot(HaveOccurred())

			listenDone := make(chan error, 1)
			go func() { listenDone <- h.Listen(slow.set) }()
			Eventually(entered).Should(Receive())

			published := make(chan error, 1)
			go func() { published <- h.Publish("jobs.x", "during") }()
			Eventually(published, time.Second).Should(Receive(BeNil()))

			subscribed := make(chan error, 1)
			go func() {
				_, err := h.Subscribe("jobs.z", func([]byte) {})
				subscribed <- err
			}()
			Consistently(listenDone, 50*time.Millisecond).ShouldNot(Receive())

			close(gate)
			Eventually(listenDone, time.Second).Should(Receive(BeNil()))
			Eventually(subscribed, time.Second).Should(Receive(BeNil()))
		})

		It("does not block a publish already running on the old carrier when the pointer moves", func() {
			stuck := newFakeCarrier(cluster.CarrierNATS, 1)
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			stuck.set.Broadcaster = blockingPublisher{Broadcaster: stuck.bus, gate: gate, entered: entered}
			cur.Store(stuck.set)

			first := make(chan error, 1)
			go func() { first <- h.Publish("jobs.x", "old") }()
			Eventually(entered).Should(Receive())

			cur.Store(b.set)
			Expect(h.Publish("jobs.x", "new")).To(Succeed())
			Expect(b.bus.PublishCount("jobs.x")).To(Equal(1))

			close(gate)
			Eventually(first, time.Second).Should(Receive(BeNil()))
		})

		It("neither loses nor repeats a subscription that races a Listen", func() {
			const n = 40
			var wg sync.WaitGroup
			counts := make([]*received, n)
			for i := range counts {
				counts[i] = &received{}
			}
			start := make(chan struct{})
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, err := h.Subscribe("jobs.x", counts[i].handler())
					Expect(err).ToNot(HaveOccurred())
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				Expect(h.Listen(b.set)).To(Succeed())
			}()
			close(start)
			wg.Wait()

			Expect(a.bus.Publish("jobs.x", "a")).To(Succeed())
			Expect(b.bus.Publish("jobs.x", "b")).To(Succeed())
			for i := range counts {
				Expect(counts[i].all()).To(ConsistOf(`"a"`, `"b"`), "subscription %d", i)
			}
		})
	})

	Describe("hot path", func() {
		It("adds no allocation to a publish", func() {
			quiet := &quietBroadcaster{}
			c := newFakeCarrier(cluster.CarrierNATS, 1)
			c.set.Broadcaster = quiet
			var p atomic.Pointer[carrier.Set]
			p.Store(c.set)
			holder := carrier.NewBroadcaster(&p)

			var payload any = "payload"
			allocs := testing.AllocsPerRun(1000, func() {
				_ = holder.Publish("jobs.x", payload)
			})
			Expect(allocs).To(BeZero())
			Expect(quiet.published.Load()).To(BeNumerically(">", 1000))
		})
	})
})

var _ messaging.Broadcaster = failingBroadcaster{}

// failingBroadcaster refuses Subscribe.
type failingBroadcaster struct {
	messaging.Broadcaster
	err error
}

func (f failingBroadcaster) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	return nil, f.err
}

// blockingBroadcaster blocks Subscribe until gate closes.
type blockingBroadcaster struct {
	messaging.Broadcaster
	gate    chan struct{}
	entered chan struct{}
}

func (f blockingBroadcaster) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	select {
	case f.entered <- struct{}{}:
	default:
	}
	<-f.gate
	return f.Broadcaster.Subscribe(subject, handler)
}

// blockingPublisher blocks Publish until gate closes.
type blockingPublisher struct {
	messaging.Broadcaster
	gate    chan struct{}
	entered chan struct{}
}

func (f blockingPublisher) Publish(subject string, data any) error {
	select {
	case f.entered <- struct{}{}:
	default:
	}
	<-f.gate
	return f.Broadcaster.Publish(subject, data)
}

// quietBroadcaster counts publishes without allocating.
type quietBroadcaster struct{ published atomic.Int64 }

func (q *quietBroadcaster) Publish(string, any) error { q.published.Add(1); return nil }
func (q *quietBroadcaster) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}

var _ = Describe("Broadcaster holder subjects", func() {
	var (
		a, b *fakeCarrier
		cur  atomic.Pointer[carrier.Set]
		h    *carrier.Broadcaster
	)

	BeforeEach(func() {
		a = newFakeCarrier(cluster.CarrierNATS, 1)
		b = newFakeCarrier(cluster.CarrierTunnel, 2)
		cur = atomic.Pointer[carrier.Set]{}
		cur.Store(a.set)
		h = carrier.NewBroadcaster(&cur)
	})

	// Control traffic has its own clients and never goes through the holder. A
	// carrier with no request and reply refuses these roots, so a subscription
	// that the holder accepted would fail the first Listen onto that carrier,
	// long after the call that caused it.
	DescribeTable("refuses a subscription to a control root when it is made",
		func(subject string) {
			got := &received{}
			sub, err := h.Subscribe(subject, got.handler())
			Expect(err).To(MatchError(messaging.ErrUnservedSubject))
			Expect(sub).To(BeNil())
			_ = a.bus.Publish(subject, "x")
			Expect(got.all()).To(BeEmpty(), "the refused subscription was attached anyway")

			Expect(h.Listen(b.set)).To(Succeed(), "a refused subscription must not stay registered for a later swap")
		},
		Entry("nodes", "nodes.x.backend.install"),
		Entry("mcp", "mcp.tools.execute"),
		Entry("a subject outside every root", "elsewhere.x"),
	)

	It("accepts a subscription to a broadcast root", func() {
		sub, err := h.Subscribe("jobs.x.progress", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub).ToNot(BeNil())
	})
})
