package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/messaging/messagingtest"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// droppingBus accepts every publish and delivers none, as a bus that loses its
// messages does.
type droppingBus struct{ messaging.Broadcaster }

func (droppingBus) Publish(string, any) error { return nil }

// brokenBus refuses every publish.
type brokenBus struct{ messaging.Broadcaster }

func (brokenBus) Publish(string, any) error { return errors.New("the bus is down") }

type consumerOptions struct {
	owner    string
	events   messaging.Publisher
	hints    messaging.Broadcaster
	interval time.Duration
}

// newConsumer returns a consumer for a replica that is live.
func newConsumer(db *gorm.DB, o consumerOptions) *ClaimConsumer {
	GinkgoHelper()
	live(db, o.owner)
	if o.events == nil {
		o.events = testutil.NewFakeBus()
	}
	c, err := NewClaimConsumer(ClaimConsumerConfig{
		DB: db, Owner: o.owner, Events: o.events, Hints: o.hints, Interval: o.interval,
	})
	Expect(err).ToNot(HaveOccurred())
	return c
}

var _ = Describe("The claim queue as a WorkQueue", func() {
	messagingtest.RunWorkQueueConformance(func() messagingtest.WorkQueueRig {
		db := newClaimDB()
		bus := testutil.NewFakeBus()
		var n atomic.Int32
		return messagingtest.WorkQueueRig{
			Queue: NewClaimQueue(db, bus),
			NewConsumer: func() messaging.WorkConsumer {
				return newConsumer(db, consumerOptions{
					owner:    fmt.Sprintf("replica-%d", n.Add(1)),
					hints:    bus,
					interval: 100 * time.Millisecond,
				})
			},
		}
	})
})

var _ = Describe("The claim queue", func() {
	var (
		ctx context.Context
		db  *gorm.DB
		bus *testutil.FakeBus
	)

	BeforeEach(func() {
		c, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ctx = c
		db = newClaimDB()
		bus = testutil.NewFakeBus()
	})

	consume := func(c *ClaimConsumer, kind messaging.WorkKind, max int, h messaging.WorkHandler) messaging.Subscription {
		GinkgoHelper()
		sub, err := c.Consume(ctx, kind, max, h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = sub.Unsubscribe() })
		return sub
	}

	rows := func() int64 {
		var n int64
		Expect(db.Model(&WorkClaim{}).Count(&n).Error).To(Succeed())
		return n
	}

	Describe("the wake hint", func() {
		It("starts a consumer at once, with a poll interval that would have kept it waiting for seconds", func() {
			picked := make(chan time.Time, 64)
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 2 * time.Second})
			consume(c, messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error {
				picked <- time.Now()
				return nil
			})
			queue := NewClaimQueue(db, bus)

			var waits []time.Duration
			for i := range 20 {
				time.Sleep(20 * time.Millisecond)
				start := time.Now()
				Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, i)).To(Succeed())
				var at time.Time
				Eventually(picked, 5*time.Second).Should(Receive(&at))
				waits = append(waits, at.Sub(start))
			}
			sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
			Expect(waits[len(waits)/2]).To(BeNumerically("<", 50*time.Millisecond), "the p50 of the pickup, waits: %v", waits)
		})

		It("loses no work when every hint is dropped, because the poll finds it", func() {
			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 150 * time.Millisecond})
			consume(c, messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error {
				got.Add(1)
				return nil
			})
			queue := NewClaimQueue(db, droppingBus{bus})
			for i := range 10 {
				Expect(queue.Enqueue(ctx, messaging.WorkAgentRun, i)).To(Succeed())
			}
			Eventually(got.Load, 5*time.Second).Should(BeEquivalentTo(10))
			Eventually(rows, 5*time.Second).Should(BeZero())
		})

		It("enqueues when the bus is down, and says nothing of the hint", func() {
			queue := NewClaimQueue(db, brokenBus{bus})
			Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, "x")).To(Succeed())
			Expect(rows()).To(BeEquivalentTo(1))
		})

		It("enqueues with no bus at all", func() {
			Expect(NewClaimQueue(db, nil).Enqueue(ctx, messaging.WorkMCPCI, "x")).To(Succeed())
			Expect(rows()).To(BeEquivalentTo(1))
		})

		It("names the kind in the hint, on the subject that every carrier serves", func() {
			var got []byte
			_, err := bus.Subscribe(messaging.SubjectClaimWake, func(b []byte) { got = b })
			Expect(err).ToNot(HaveOccurred())
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkAgentRun, 1)).To(Succeed())
			Expect(got).To(MatchJSON(`{"kind":"agent-run"}`))
			Expect(messaging.ValidateBroadcastSubject(messaging.SubjectClaimWake)).To(Succeed())
		})

		It("does not wake the consumers of another kind", func() {
			// The poll interval is long, so only a hint could start the handler.
			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: time.Hour})
			consume(c, messaging.WorkTask, 0, func(context.Context, []byte, messaging.Publisher) error {
				got.Add(1)
				return nil
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, "x")).To(Succeed())
			Consistently(got.Load, 300*time.Millisecond).Should(BeZero())
		})
	})

	Describe("work that nobody consumes yet", func() {
		It("stays in the table, and a consumer that starts later gets it", func() {
			queue := NewClaimQueue(db, bus)
			for i := range 5 {
				Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, i)).To(Succeed())
			}
			Expect(rows()).To(BeEquivalentTo(5))

			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			consume(c, messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error {
				got.Add(1)
				return nil
			})
			Eventually(got.Load, 5*time.Second).Should(BeEquivalentTo(5))
		})
	})

	Describe("exactly once under competing consumers", func() {
		It("runs every unit once when five replicas with four slots each compete for two hundred", func() {
			const items = 200
			var mu sync.Mutex
			seen := map[string]int{}
			handler := func(_ context.Context, p []byte, _ messaging.Publisher) error {
				var j struct{ N int }
				Expect(json.Unmarshal(p, &j)).To(Succeed())
				time.Sleep(time.Millisecond)
				mu.Lock()
				seen[fmt.Sprint(j.N)]++
				mu.Unlock()
				return nil
			}
			for r := range 5 {
				c := newConsumer(db, consumerOptions{owner: fmt.Sprintf("replica-%d", r), hints: bus, interval: 100 * time.Millisecond})
				consume(c, messaging.WorkAgentRun, 4, handler)
			}
			queue := NewClaimQueue(db, bus)
			for i := range items {
				Expect(queue.Enqueue(ctx, messaging.WorkAgentRun, map[string]int{"N": i})).To(Succeed())
			}
			Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(seen) }, 30*time.Second).Should(Equal(items))
			Eventually(rows, 10*time.Second).Should(BeZero())
			mu.Lock()
			defer mu.Unlock()
			for n, count := range seen {
				Expect(count).To(Equal(1), "unit %s", n)
			}
		})
	})

	Describe("order", func() {
		It("hands the units of a kind to a serial consumer in the order they were enqueued", func() {
			var mu sync.Mutex
			var order []int
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			queue := NewClaimQueue(db, bus)
			for i := range 25 {
				Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, i)).To(Succeed())
			}
			consume(c, messaging.WorkMCPCI, 1, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				var n int
				Expect(json.Unmarshal(p, &n)).To(Succeed())
				mu.Lock()
				order = append(order, n)
				mu.Unlock()
				return nil
			})
			Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(order) }, 15*time.Second).Should(Equal(25))
			mu.Lock()
			defer mu.Unlock()
			Expect(sort.IntsAreSorted(order)).To(BeTrue(), "got %v", order)
		})

		It("keeps the order of each kind apart from the others", func() {
			queue := NewClaimQueue(db, bus)
			for i := range 6 {
				Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, i)).To(Succeed())
				Expect(queue.Enqueue(ctx, messaging.WorkAgentRun, 100+i)).To(Succeed())
			}
			var mu sync.Mutex
			got := map[messaging.WorkKind][]int{}
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			for _, kind := range []messaging.WorkKind{messaging.WorkMCPCI, messaging.WorkAgentRun} {
				consume(c, kind, 1, func(_ context.Context, p []byte, _ messaging.Publisher) error {
					var n int
					_ = json.Unmarshal(p, &n)
					mu.Lock()
					got[kind] = append(got[kind], n)
					mu.Unlock()
					return nil
				})
			}
			Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(got[messaging.WorkMCPCI]) + len(got[messaging.WorkAgentRun]) }, 15*time.Second).Should(Equal(12))
			mu.Lock()
			defer mu.Unlock()
			Expect(got[messaging.WorkMCPCI]).To(Equal([]int{0, 1, 2, 3, 4, 5}))
			Expect(got[messaging.WorkAgentRun]).To(Equal([]int{100, 101, 102, 103, 104, 105}))
		})
	})

	Describe("a replica that dies in the middle of a job", func() {
		It("returns the job to the queue and runs it on another replica, with no loss", func() {
			started := make(chan struct{})
			hold := make(chan struct{})
			var ranOnA, ranOnB atomic.Int32

			a := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			consume(a, messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error {
				ranOnA.Add(1)
				close(started)
				<-hold // the process is gone: this never returns during the spec
				return nil
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkAgentRun, "important")).To(Succeed())
			Eventually(started, 5*time.Second).Should(BeClosed())
			Expect(rows()).To(BeEquivalentTo(1))

			// A stops heartbeating. B is another replica, and it was there all along.
			kill(db, "replica-a")
			b := newConsumer(db, consumerOptions{owner: "replica-b", hints: bus, interval: 100 * time.Millisecond})
			consume(b, messaging.WorkAgentRun, 0, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				ranOnB.Add(1)
				Expect(string(p)).To(Equal(`"important"`))
				return nil
			})
			Eventually(ranOnB.Load, 10*time.Second).Should(BeEquivalentTo(1))
			Eventually(rows, 5*time.Second).Should(BeZero())

			// The late answer of A changes nothing.
			close(hold)
			Consistently(rows, 300*time.Millisecond).Should(BeZero())
			Expect(ranOnA.Load()).To(BeEquivalentTo(1))
			Expect(ranOnB.Load()).To(BeEquivalentTo(1), "the job ran once more on B, which is what at-least-once means")
		})

		It("does not take a job from a replica that only runs it slowly", func() {
			started := make(chan struct{})
			hold := make(chan struct{})
			var ranOnB atomic.Int32
			a := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			consume(a, messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error {
				close(started)
				<-hold
				return nil
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkAgentRun, "slow")).To(Succeed())
			Eventually(started, 5*time.Second).Should(BeClosed())
			Expect(db.Exec(`UPDATE work_claims SET claimed_at = now() - interval '2 hours'`).Error).To(Succeed())

			b := newConsumer(db, consumerOptions{owner: "replica-b", hints: bus, interval: 50 * time.Millisecond})
			consume(b, messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error {
				ranOnB.Add(1)
				return nil
			})
			Consistently(ranOnB.Load, time.Second).Should(BeZero())
			close(hold)
			Eventually(rows, 5*time.Second).Should(BeZero())
		})

		It("recovers every job it held, and the ones still waiting go to the others", func() {
			const jobs = 12
			hold := make(chan struct{})
			defer close(hold)
			var held atomic.Int32
			a := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			consume(a, messaging.WorkMCPCI, 3, func(context.Context, []byte, messaging.Publisher) error {
				held.Add(1)
				<-hold
				return nil
			})
			queue := NewClaimQueue(db, bus)
			for i := range jobs {
				Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, i)).To(Succeed())
			}
			Eventually(held.Load, 5*time.Second).Should(BeEquivalentTo(3))
			kill(db, "replica-a")

			var mu sync.Mutex
			ran := map[int]int{}
			b := newConsumer(db, consumerOptions{owner: "replica-b", hints: bus, interval: 100 * time.Millisecond})
			consume(b, messaging.WorkMCPCI, 4, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				var n int
				_ = json.Unmarshal(p, &n)
				mu.Lock()
				ran[n]++
				mu.Unlock()
				return nil
			})
			Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(ran) }, 15*time.Second).Should(Equal(jobs))
			Eventually(rows, 5*time.Second).Should(BeZero())
		})

		It("claims nothing while its own row says that it is not live", func() {
			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			kill(db, "replica-a")
			consume(c, messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error {
				got.Add(1)
				return nil
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, 1)).To(Succeed())
			Consistently(got.Load, 500*time.Millisecond).Should(BeZero())

			live(db, "replica-a") // its membership registers
			Eventually(got.Load, 5*time.Second).Should(BeEquivalentTo(1))
		})
	})

	Describe("a unit of work that fails", func() {
		It("goes back to the queue with a wait, and the work behind it is not held up", func() {
			var mu sync.Mutex
			runs := map[string]int{}
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			consume(c, messaging.WorkMCPCI, 1, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				mu.Lock()
				runs[string(p)]++
				mu.Unlock()
				if string(p) == `"poison"` {
					return errors.New("cannot serve")
				}
				return nil
			})
			queue := NewClaimQueue(db, bus)
			Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, "poison")).To(Succeed())
			for i := range 5 {
				Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, fmt.Sprintf("ok-%d", i))).To(Succeed())
			}
			Eventually(func() int {
				mu.Lock()
				defer mu.Unlock()
				n := 0
				for k, v := range runs {
					if k != `"poison"` {
						n += v
					}
				}
				return n
			}, 10*time.Second).Should(Equal(5))

			Consistently(func() int { mu.Lock(); defer mu.Unlock(); return runs[`"poison"`] }, time.Second).Should(BeNumerically("<=", 2),
				"a wait of at least two seconds keeps it from coming back at the poll interval")
			Expect(rows()).To(BeEquivalentTo(1))
			var row WorkClaim
			Expect(db.First(&row).Error).To(Succeed())
			Expect(row.Attempts).To(BeNumerically(">=", 1))
			Expect(row.State).To(Equal(ClaimPending))
		})

		It("survives a handler that panics, and gives the row the same wait", func() {
			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			consume(c, messaging.WorkAgentRun, 0, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				if string(p) == `"bomb"` {
					panic("bug in the handler")
				}
				got.Add(1)
				return nil
			})
			queue := NewClaimQueue(db, bus)
			Expect(queue.Enqueue(ctx, messaging.WorkAgentRun, "bomb")).To(Succeed())
			Expect(queue.Enqueue(ctx, messaging.WorkAgentRun, "fine")).To(Succeed())
			Eventually(got.Load, 5*time.Second).Should(BeEquivalentTo(1))
			Eventually(func() int {
				var row WorkClaim
				if err := db.First(&row).Error; err != nil {
					return -1
				}
				return row.Attempts
			}, 5*time.Second).Should(BeNumerically(">=", 1))
		})

		It("is dropped for good when the handler says it could not read the payload, as a handler returns nil for that", func() {
			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			consume(c, messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error {
				got.Add(1)
				return nil
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkAgentRun, "garbage")).To(Succeed())
			Eventually(got.Load, 5*time.Second).Should(BeEquivalentTo(1))
			Eventually(rows, 5*time.Second).Should(BeZero())
			Consistently(got.Load, 300*time.Millisecond).Should(BeEquivalentTo(1))
		})

		It("stays with its replica when the answer could not be recorded, because running it again would repeat the work", func() {
			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			consume(c, messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error {
				got.Add(1)
				return fmt.Errorf("the store refused the result: %w", ErrKeepClaim)
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, 1)).To(Succeed())
			Eventually(got.Load, 5*time.Second).Should(BeEquivalentTo(1))
			Consistently(got.Load, time.Second).Should(BeEquivalentTo(1))
			var row WorkClaim
			Expect(db.First(&row).Error).To(Succeed())
			Expect(row.State).To(Equal(ClaimClaimed))
			Expect(row.ClaimedBy).To(Equal("replica-a"))

			// If the replica dies, the work is taken over: the answer is lost with it.
			kill(db, "replica-a")
			var ranOnB atomic.Int32
			b := newConsumer(db, consumerOptions{owner: "replica-b", hints: bus, interval: 50 * time.Millisecond})
			consume(b, messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error {
				ranOnB.Add(1)
				return nil
			})
			Eventually(ranOnB.Load, 5*time.Second).Should(BeEquivalentTo(1))
		})
	})

	Describe("a consumer", func() {
		It("hands its handlers the publisher it was built with, and the context it was given", func() {
			events := testutil.NewFakeBus()
			type ctxKey struct{}
			parent := context.WithValue(ctx, ctxKey{}, "marker")
			var gotEvents messaging.Publisher
			var marker atomic.Value
			done := make(chan struct{})
			c := newConsumer(db, consumerOptions{owner: "replica-a", events: events, hints: bus, interval: 50 * time.Millisecond})
			sub, err := c.Consume(parent, messaging.WorkMCPCI, 0, func(ctx context.Context, _ []byte, e messaging.Publisher) error {
				gotEvents = e
				marker.Store(ctx.Value(ctxKey{}))
				close(done)
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, 1)).To(Succeed())
			Eventually(done, 5*time.Second).Should(BeClosed())
			Expect(gotEvents).To(BeIdenticalTo(events))
			Expect(marker.Load()).To(Equal("marker"))
		})

		It("settles the claim of a handler that runs when the replica is told to stop, and Unsubscribe waits for it", func() {
			started := make(chan struct{})
			release := make(chan struct{})
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			sub, err := c.Consume(ctx, messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error {
				close(started)
				<-release
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, 1)).To(Succeed())
			Eventually(started, 5*time.Second).Should(BeClosed())

			stopped := make(chan struct{})
			go func() { defer close(stopped); _ = sub.Unsubscribe() }()
			Consistently(stopped, 200*time.Millisecond).ShouldNot(BeClosed())
			close(release)
			Eventually(stopped, 5*time.Second).Should(BeClosed())
			Expect(rows()).To(BeZero(), "the claim was settled before Unsubscribe returned")
		})

		It("settles a claim after the context of the replica ended, so a restart does not leave it held", func() {
			started := make(chan struct{})
			parent, stop := context.WithCancel(ctx)
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			sub, err := c.Consume(parent, messaging.WorkMCPCI, 0, func(ctx context.Context, _ []byte, _ messaging.Publisher) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, 1)).To(Succeed())
			Eventually(started, 5*time.Second).Should(BeClosed())
			stop()
			Expect(sub.Unsubscribe()).To(Succeed())

			var row WorkClaim
			Expect(db.First(&row).Error).To(Succeed())
			Expect(row.State).To(Equal(ClaimPending), "released, not left held by a replica that is leaving")
		})

		It("refuses a kind that is not a work kind", func() {
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus})
			_, err := c.Consume(ctx, messaging.WorkKind("nonsense"), 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			Expect(err).To(HaveOccurred())
		})

		It("is built only with what it needs", func() {
			good := ClaimConsumerConfig{DB: db, Owner: "replica-a", Events: bus}
			for name, mutate := range map[string]func(*ClaimConsumerConfig){
				"no database": func(c *ClaimConsumerConfig) { c.DB = nil },
				"no owner":    func(c *ClaimConsumerConfig) { c.Owner = "" },
				"no events":   func(c *ClaimConsumerConfig) { c.Events = nil },
			} {
				cfg := good
				mutate(&cfg)
				_, err := NewClaimConsumer(cfg)
				Expect(err).To(HaveOccurred(), name)
			}
			_, err := NewClaimConsumer(good)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("the order of the kinds in the table", func() {
		It("serves a kind that is the only one with work, whatever else is waiting", func() {
			queue := NewClaimQueue(db, bus)
			for i := range 3 {
				Expect(queue.Enqueue(ctx, messaging.WorkTask, i)).To(Succeed())
			}
			var got atomic.Int32
			c := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 50 * time.Millisecond})
			consume(c, messaging.WorkAgentRun, 0, func(context.Context, []byte, messaging.Publisher) error { got.Add(1); return nil })
			Expect(queue.Enqueue(ctx, messaging.WorkAgentRun, 1)).To(Succeed())
			Eventually(got.Load, 5*time.Second).Should(BeEquivalentTo(1))
			Expect(rows()).To(BeEquivalentTo(3))
		})
	})
})

