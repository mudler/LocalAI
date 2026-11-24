package carrier_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
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
		pg, nats *testutil.FakeBus
		a, b     *frontend
	)

	BeforeEach(func() {
		pg, nats = testutil.NewFakeBus(), testutil.NewFakeBus()
		a, b = newFrontend(pg, nats), newFrontend(pg, nats)
	})

	It("refuses to be built without a floor, or with a bus that cannot broadcast", func() {
		_, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{})
		Expect(err).To(HaveOccurred())
		_, err = carrier.NewBroadcaster(carrier.BroadcasterOptions{Floor: fakeFloor(pg), Primary: &carrier.Bus{Name: carrier.BusNATS}})
		Expect(err).To(HaveOccurred())
	})

	It("listens on both buses and publishes on NATS when this process reaches it", func() {
		got := &received{}
		_, err := a.h.Subscribe("staging.x", got.handler())
		Expect(err).ToNot(HaveOccurred())

		Expect(b.h.Publish("staging.x", "one")).To(Succeed())
		Expect(nats.PublishCount("staging.x")).To(Equal(1))
		Expect(pg.PublishCount("staging.x")).To(Equal(0), "one bus carries each broadcast")
		Expect(got.all()).To(Equal([]string{`"one"`}))
		Expect(b.h.PublishBus()).To(Equal(carrier.BusNATS))

		// Another process that publishes on PostgreSQL is heard too.
		Expect(pg.Publish("staging.x", "two")).To(Succeed())
		Expect(got.all()).To(Equal([]string{`"one"`, `"two"`}))
	})

	It("publishes on PostgreSQL when it does not reach NATS, and on NATS again when it is back", func() {
		got := &received{}
		_, err := b.h.Subscribe("staging.x", got.handler())
		Expect(err).ToNot(HaveOccurred())

		a.nats.down()
		Expect(a.h.PublishBus()).To(Equal(carrier.BusPostgres))
		Expect(a.h.Publish("staging.x", "via-pg")).To(Succeed())
		Expect(pg.PublishCount("staging.x")).To(Equal(1))
		Expect(nats.PublishCount("staging.x")).To(Equal(0))

		a.nats.back()
		Expect(a.h.Publish("staging.x", "via-nats")).To(Succeed())
		Expect(nats.PublishCount("staging.x")).To(Equal(1))
		Expect(got.all()).To(Equal([]string{`"via-pg"`, `"via-nats"`}), "each message once, on the bus that carried it")
	})

	It("publishes on PostgreSQL when the cluster has no NATS", func() {
		solo := newFrontend(pg, nil)
		Expect(solo.h.PublishBus()).To(Equal(carrier.BusPostgres))
		Expect(solo.h.Publish("staging.x", "x")).To(Succeed())
		Expect(pg.PublishCount("staging.x")).To(Equal(1))
		Expect(solo.h.Unready()).To(Succeed(), "no bus is configured that it cannot reach")
	})

	It("is unready while a configured NATS is out of reach, and ready again when it is back", func() {
		Expect(a.h.Unready()).To(Succeed())
		a.nats.down()
		err := a.h.Unready()
		Expect(err).To(MatchError(carrier.ErrBusUnreachable))
		Expect(err.Error()).To(ContainSubstring("nats"))
		Expect(err.Error()).To(ContainSubstring("postgres"))
		a.nats.back()
		Expect(a.h.Unready()).To(Succeed())
	})

	It("runs the reconnect hooks when publishes move to another bus, and when NATS is back", func() {
		var n atomic.Int32
		a.h.OnReconnect(func() { n.Add(1) })
		Expect(a.h.Publish("staging.x", "first")).To(Succeed())
		Expect(n.Load()).To(BeZero(), "the first publish moves nothing")

		a.nats.down()
		Expect(a.h.Publish("staging.x", "lost")).To(Succeed())
		Eventually(n.Load).Should(Equal(int32(1)), "messages across the change are not ordered")

		a.nats.back()
		Eventually(n.Load).Should(Equal(int32(2)), "the reconnect of NATS runs the hooks")
		Expect(a.h.Publish("staging.x", "again")).To(Succeed())
		Eventually(n.Load).Should(Equal(int32(3)), "and so does the move back to NATS")
	})

	It("removes a subscription from every bus when it is unsubscribed", func() {
		got := &received{}
		sub, err := a.h.Subscribe("staging.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		Expect(sub.Unsubscribe()).To(Succeed())
		Expect(pg.Publish("staging.x", "a")).To(Succeed())
		Expect(nats.Publish("staging.x", "b")).To(Succeed())
		Expect(got.all()).To(BeEmpty())

		// It stays gone for a bus attached later.
		extra := testutil.NewFakeBus()
		Expect(a.h.ListenBus(fakeFloor(extra))).To(Succeed())
		Expect(extra.Publish("staging.x", "c")).To(Succeed())
		Expect(got.all()).To(BeEmpty())
	})

	It("tolerates a second Unsubscribe", func() {
		sub, err := a.h.Subscribe("staging.x", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub.Unsubscribe()).To(Succeed())
		Expect(sub.Unsubscribe()).To(Succeed())
	})

	It("treats a second Listen of the same bus as nothing to do", func() {
		got := &received{}
		_, err := a.h.Subscribe("staging.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		extra := testutil.NewFakeBus()
		m := fakeFloor(extra)
		Expect(a.h.ListenBus(m)).To(Succeed())
		Expect(a.h.ListenBus(m)).To(Succeed())
		Expect(extra.Publish("staging.x", "x")).To(Succeed())
		Expect(got.all()).To(HaveLen(1), "one subscription per bus, not two")
	})

	It("fails a subscription when a bus refuses it, and undoes the part that worked", func() {
		boom := errors.New("subscribe refused")
		h, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{
			Floor:   fakeFloor(pg),
			Primary: &carrier.Bus{Name: carrier.BusNATS, Broadcaster: failingBroadcaster{Broadcaster: nats, err: boom}},
		})
		Expect(err).ToNot(HaveOccurred())
		got := &received{}
		_, err = h.Subscribe("staging.x", got.handler())
		Expect(err).To(MatchError(boom))
		Expect(pg.Publish("staging.x", "a")).To(Succeed())
		Expect(got.all()).To(BeEmpty(), "the half-made subscription was removed")
	})

	It("detaches the subscriptions from a released bus", func() {
		got := &received{}
		_, err := a.h.Subscribe("staging.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		extra := testutil.NewFakeBus()
		m := fakeFloor(extra)
		Expect(a.h.ListenBus(m)).To(Succeed())
		Expect(a.h.ReleaseBus(m)).To(Succeed())
		Expect(extra.Publish("staging.x", "x")).To(Succeed())
		Expect(got.all()).To(BeEmpty())
		Expect(a.h.ReleaseBus(m)).To(Succeed(), "releasing a bus that is not attached is harmless")
	})

	It("closes its buses and hears nothing after Close", func() {
		closed := 0
		floor := fakeFloor(pg)
		floor.Close = func() { closed++ }
		h, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{Floor: floor})
		Expect(err).ToNot(HaveOccurred())
		got := &received{}
		_, err = h.Subscribe("staging.x", got.handler())
		Expect(err).ToNot(HaveOccurred())
		h.Close()
		Expect(closed).To(Equal(1))
		Expect(pg.Publish("staging.x", "x")).To(Succeed())
		Expect(got.all()).To(BeEmpty())
	})

	Describe("reconnect hooks", func() {
		It("runs a hook when either bus reconnects", func() {
			var n atomic.Int32
			a.h.OnReconnect(func() { n.Add(1) })
			pg.TriggerReconnect()
			Expect(n.Load()).To(Equal(int32(1)))
			a.nats.back()
			Expect(n.Load()).To(Equal(int32(2)))
		})

		It("runs a hook once per reconnect however often a bus is attached", func() {
			var n atomic.Int32
			solo := newFrontend(testutil.NewFakeBus(), nil)
			solo.h.OnReconnect(func() { n.Add(1) })
			extra := testutil.NewFakeBus()
			m := fakeFloor(extra)
			Expect(solo.h.ListenBus(m)).To(Succeed())
			Expect(solo.h.ReleaseBus(m)).To(Succeed())
			Expect(solo.h.ListenBus(m)).To(Succeed())
			extra.TriggerReconnect()
			Expect(n.Load()).To(Equal(int32(1)))
		})

		It("ignores a nil hook", func() {
			a.h.OnReconnect(nil)
			pg.TriggerReconnect()
		})

		It("lets the caller run every hook once", func() {
			var n atomic.Int32
			a.h.OnReconnect(func() { n.Add(1) })
			a.h.OnReconnect(func() { n.Add(1) })
			a.h.NotifyReconnect()
			Expect(n.Load()).To(Equal(int32(2)))
		})

		It("works with a bus that has no reconnect notion", func() {
			Expect(a.h.ListenBus(&carrier.Bus{Name: "plain", Broadcaster: testutil.NewFakeBus()})).To(Succeed())
		})
	})

	Describe("while a bus is attached", func() {
		It("does not hold a publish behind a listen that is waiting on a bus", func() {
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			slow := &carrier.Bus{Name: "slow", Broadcaster: blockingBroadcaster{Broadcaster: testutil.NewFakeBus(), gate: gate, entered: entered}}

			_, err := a.h.Subscribe("staging.x", func([]byte) {})
			Expect(err).ToNot(HaveOccurred())

			listenDone := make(chan error, 1)
			go func() { listenDone <- a.h.ListenBus(slow) }()
			Eventually(entered).Should(Receive())

			published := make(chan error, 1)
			go func() { published <- a.h.Publish("staging.x", "during") }()
			Eventually(published, time.Second).Should(Receive(BeNil()))

			subscribed := make(chan error, 1)
			go func() {
				_, err := a.h.Subscribe("staging.z", func([]byte) {})
				subscribed <- err
			}()
			Consistently(listenDone, 50*time.Millisecond).ShouldNot(Receive())

			close(gate)
			Eventually(listenDone, time.Second).Should(Receive(BeNil()))
			Eventually(subscribed, time.Second).Should(Receive(BeNil()))
		})

		It("neither loses nor repeats a subscription that races a Listen", func() {
			const n = 40
			var wg sync.WaitGroup
			counts := make([]*received, n)
			for i := range counts {
				counts[i] = &received{}
			}
			extra := testutil.NewFakeBus()
			start := make(chan struct{})
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, err := a.h.Subscribe("staging.x", counts[i].handler())
					Expect(err).ToNot(HaveOccurred())
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				Expect(a.h.ListenBus(fakeFloor(extra))).To(Succeed())
			}()
			close(start)
			wg.Wait()

			Expect(pg.Publish("staging.x", "a")).To(Succeed())
			Expect(extra.Publish("staging.x", "b")).To(Succeed())
			for i := range counts {
				Expect(counts[i].all()).To(ConsistOf(`"a"`, `"b"`), "subscription %d", i)
			}
		})
	})

	Describe("hot path", func() {
		It("adds no allocation to a publish", func() {
			quiet := &quietBroadcaster{}
			holder, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{
				Floor:   &carrier.Bus{Name: carrier.BusPostgres, Broadcaster: &quietBroadcaster{}},
				Primary: &carrier.Bus{Name: carrier.BusNATS, Broadcaster: quiet, Connected: func() bool { return true }},
			})
			Expect(err).ToNot(HaveOccurred())

			var payload any = "payload"
			allocs := testing.AllocsPerRun(1000, func() {
				_ = holder.Publish("staging.x", payload)
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

// quietBroadcaster counts publishes without allocating.
type quietBroadcaster struct{ published atomic.Int64 }

func (q *quietBroadcaster) Publish(string, any) error { q.published.Add(1); return nil }
func (q *quietBroadcaster) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return nil, nil
}

var _ = Describe("Broadcaster holder subjects", func() {
	var (
		pg *testutil.FakeBus
		h  *carrier.Broadcaster
	)

	BeforeEach(func() {
		pg = testutil.NewFakeBus()
		h = newFrontend(pg, testutil.NewFakeBus()).h
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
			_ = pg.Publish(subject, "x")
			Expect(got.all()).To(BeEmpty(), "the refused subscription was attached anyway")

			Expect(h.ListenBus(fakeFloor(testutil.NewFakeBus()))).To(Succeed(), "a refused subscription must not stay registered for a later bus")
		},
		Entry("nodes", "nodes.x.backend.install"),
		Entry("mcp", "mcp.tools.execute"),
		Entry("a subject outside every root", "elsewhere.x"),
	)

	It("accepts a subscription to a broadcast root", func() {
		sub, err := h.Subscribe("staging.x.progress", func([]byte) {})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub).ToNot(BeNil())
	})
})
