package messagingtest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// WorkQueueRig is what a factory returns for the work queue suite: one queue,
// with a producer and as many competing consumers as the suite asks for.
type WorkQueueRig struct {
	// Queue is the producer.
	Queue messaging.WorkQueue
	// NewConsumer returns a consumer that competes with every other consumer of
	// the rig for the work of Queue, as one more worker would.
	NewConsumer func() messaging.WorkConsumer
	// Cleanup releases the rig.
	Cleanup func()
}

// WorkQueueFactory returns a ready rig. A factory that cannot build one calls
// Skip or Fail itself.
type WorkQueueFactory func() WorkQueueRig

type job struct {
	ID   string `json:"id"`
	Data string `json:"data,omitempty"`
}

// RunWorkQueueConformance registers the suite every WorkQueue carrier must pass.
//
// It states what the carriers share: one consumer receives a payload, competing
// consumers split the work, the kinds are apart, the limit on concurrent
// handlers holds, and the same payload bound applies. It does not state what
// differs and what the contract leaves to the carrier: what happens to a unit of
// work whose consumer dies, and how often a handler that failed is called again.
// Handlers must tolerate a repeat.
func RunWorkQueueConformance(newRig WorkQueueFactory) {
	Describe("WorkQueue conformance", func() {
		var (
			rig WorkQueueRig
			ctx context.Context
		)

		BeforeEach(func() {
			rig = newRig()
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			DeferCleanup(cancel)
		})
		// A factory that skips (no Docker for the NATS run) returns before it sets
		// a cleanup, so the guard keeps a skip from turning into a panic.
		AfterEach(func() {
			if rig.Cleanup != nil {
				rig.Cleanup()
			}
		})

		// seen keeps the ids a handler has been given.
		type seen struct {
			mu  sync.Mutex
			ids []string
		}
		add := func(s *seen, id string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.ids = append(s.ids, id)
		}
		count := func(s *seen) func() int {
			return func() int {
				s.mu.Lock()
				defer s.mu.Unlock()
				return len(s.ids)
			}
		}

		consume := func(c messaging.WorkConsumer, kind messaging.WorkKind, max int, h messaging.WorkHandler) messaging.Subscription {
			GinkgoHelper()
			sub, err := c.Consume(ctx, kind, max, h)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })
			return sub
		}

		It("gives the payload to a consumer of the kind, with a publisher for its events", func() {
			var got seen
			var payload atomic.Value
			var hadEvents atomic.Bool
			consume(rig.NewConsumer(), messaging.WorkMCPCI, 0, func(_ context.Context, p []byte, events messaging.Publisher) error {
				payload.Store(append([]byte(nil), p...))
				hadEvents.Store(events != nil)
				add(&got, "x")
				return nil
			})

			Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, job{ID: "j1", Data: "hello"})).To(Succeed())

			Eventually(count(&got), 10*time.Second).Should(Equal(1))
			Expect(payload.Load().([]byte)).To(MatchJSON(`{"id":"j1","data":"hello"}`))
			Expect(hadEvents.Load()).To(BeTrue())
		})

		It("gives each unit of work to one of the competing consumers, once, when nothing fails", func() {
			const n = 60
			var got seen
			for range 3 {
				consume(rig.NewConsumer(), messaging.WorkAgentRun, 0, func(_ context.Context, p []byte, _ messaging.Publisher) error {
					var j job
					if err := json.Unmarshal(p, &j); err != nil {
						return nil
					}
					add(&got, j.ID)
					return nil
				})
			}
			for i := range n {
				Expect(rig.Queue.Enqueue(ctx, messaging.WorkAgentRun, job{ID: "j" + string(rune('A'+i/26)) + string(rune('a'+i%26))})).To(Succeed())
			}

			Eventually(count(&got), 20*time.Second).Should(Equal(n))
			Consistently(count(&got), 500*time.Millisecond).Should(Equal(n), "no unit is given twice")
			got.mu.Lock()
			defer got.mu.Unlock()
			unique := map[string]bool{}
			for _, id := range got.ids {
				unique[id] = true
			}
			Expect(unique).To(HaveLen(n))
		})

		It("keeps the kinds apart", func() {
			var agent, ci seen
			consume(rig.NewConsumer(), messaging.WorkAgentRun, 0, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				add(&agent, string(p))
				return nil
			})
			Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, job{ID: "ci-1"})).To(Succeed())
			Consistently(count(&agent), 500*time.Millisecond).Should(BeZero())

			consume(rig.NewConsumer(), messaging.WorkMCPCI, 0, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				add(&ci, string(p))
				return nil
			})
			Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, job{ID: "ci-2"})).To(Succeed())
			Eventually(count(&ci), 10*time.Second).Should(BeNumerically(">=", 1))
			Expect(count(&agent)()).To(BeZero())
		})

		It("refuses a kind that it does not know", func() {
			err := rig.Queue.Enqueue(ctx, messaging.WorkKind("nonsense"), job{ID: "x"})
			Expect(err).To(HaveOccurred())
			_, err = rig.NewConsumer().Consume(ctx, messaging.WorkKind("nonsense"), 0, func(context.Context, []byte, messaging.Publisher) error { return nil })
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("runs no more handlers at once than it was asked for",
			func(limit, items int) {
				var running, peak, finished atomic.Int32
				release := make(chan struct{})
				consume(rig.NewConsumer(), messaging.WorkMCPCI, limit, func(context.Context, []byte, messaging.Publisher) error {
					n := running.Add(1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
					<-release
					running.Add(-1)
					finished.Add(1)
					return nil
				})
				for i := range items {
					Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, job{ID: "j" + string(rune('a'+i))})).To(Succeed())
				}

				Eventually(running.Load, 10*time.Second).Should(BeEquivalentTo(limit))
				Consistently(running.Load, 500*time.Millisecond).Should(BeEquivalentTo(limit))
				close(release)
				Eventually(finished.Load, 10*time.Second).Should(BeEquivalentTo(items))
				Expect(peak.Load()).To(BeEquivalentTo(limit))
			},
			Entry("one at a time", 1, 4),
			Entry("three at a time", 3, 7),
		)

		It("lets Unsubscribe wait for the handler that runs, and takes no more work after it", func() {
			started := make(chan struct{})
			release := make(chan struct{})
			var after seen
			sub, err := rig.NewConsumer().Consume(ctx, messaging.WorkMCPCI, 0, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				var j job
				_ = json.Unmarshal(p, &j)
				if j.ID == "first" {
					close(started)
					<-release
					return nil
				}
				add(&after, j.ID)
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, job{ID: "first"})).To(Succeed())
			Eventually(started, 10*time.Second).Should(BeClosed())

			done := make(chan struct{})
			go func() { defer close(done); _ = sub.Unsubscribe() }()
			Consistently(done, 200*time.Millisecond).ShouldNot(BeClosed(), "Unsubscribe waits for the running handler")
			close(release)
			Eventually(done, 5*time.Second).Should(BeClosed())

			Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, job{ID: "second"})).To(Succeed())
			Consistently(count(&after), 500*time.Millisecond).Should(BeZero())
		})

		It("accepts a payload up to the bound and refuses one above it", func() {
			var got atomic.Int32
			var size atomic.Int32
			consume(rig.NewConsumer(), messaging.WorkMCPCI, 0, func(_ context.Context, p []byte, _ messaging.Publisher) error {
				size.Store(int32(len(p)))
				got.Add(1)
				return nil
			})
			// The envelope of the job is a few bytes, so a margin keeps the
			// accepted payload under the bound after encoding.
			big := job{ID: "big", Data: strings.Repeat("a", messaging.MaxWorkPayloadBytes-1024)}
			Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, big)).To(Succeed())
			Eventually(got.Load, 15*time.Second).Should(BeEquivalentTo(1))
			Expect(int(size.Load())).To(BeNumerically(">", messaging.MaxWorkPayloadBytes-2048))

			tooBig := job{ID: "too-big", Data: strings.Repeat("a", messaging.MaxWorkPayloadBytes+1)}
			Expect(rig.Queue.Enqueue(ctx, messaging.WorkMCPCI, tooBig)).ToNot(Succeed())
			Consistently(got.Load, 500*time.Millisecond).Should(BeEquivalentTo(1))
		})
	})
}
