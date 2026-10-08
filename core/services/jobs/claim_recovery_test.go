package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("The claim queue when a replica loses its claims or its process", func() {
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

	consume := func(c *ClaimConsumer, kind messaging.WorkKind, h messaging.WorkHandler) {
		GinkgoHelper()
		sub, err := c.Consume(ctx, kind, 0, h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = sub.Unsubscribe() })
	}
	// skipWaits clears the wait that a release stamps, so that a spec does not wait
	// for the backoff of the queue.
	skipWaits := func() {
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond):
					_ = db.Exec(`UPDATE work_claims SET not_before = NULL`).Error
				}
			}
		}()
		DeferCleanup(func() { close(stop); <-done })
	}
	rows := func() int64 {
		var n int64
		Expect(db.Model(&WorkClaim{}).Count(&n).Error).To(Succeed())
		return n
	}

	Describe("a replica whose instances row went missing while it ran a job", func() {
		It("stops the handler of the job that a peer took over", func() {
			started := make(chan struct{})
			ended := make(chan error, 1)
			var ranOnB atomic.Int32

			a := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			consume(a, messaging.WorkMCPCI, func(hctx context.Context, _ []byte, _ messaging.Publisher) error {
				close(started)
				<-hctx.Done()
				ended <- context.Cause(hctx)
				return hctx.Err()
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, "work")).To(Succeed())
			Eventually(started, 5*time.Second).Should(BeClosed())

			// The row of A is missing for the liveness window, and B reaps its claim.
			kill(db, "replica-a")
			b := newConsumer(db, consumerOptions{owner: "replica-b", hints: bus, interval: 100 * time.Millisecond})
			consume(b, messaging.WorkMCPCI, func(context.Context, []byte, messaging.Publisher) error {
				ranOnB.Add(1)
				return nil
			})
			Eventually(ranOnB.Load, 10*time.Second).Should(BeEquivalentTo(1))

			var cause error
			Eventually(ended, 5*time.Second).Should(Receive(&cause), "the handler on A is told to stop, it does not run on")
			Expect(errors.Is(cause, errClaimLost) || errors.Is(cause, errOwnerNotLive)).To(BeTrue(), "cause: %v", cause)
			Eventually(rows, 5*time.Second).Should(BeZero())
		})

		It("gives the claim back when the replica registers again before a peer reaped it, and the job runs again", func() {
			firstEnded := make(chan struct{})
			var calls atomic.Int32
			a := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			consume(a, messaging.WorkMCPCI, func(hctx context.Context, _ []byte, _ messaging.Publisher) error {
				if calls.Add(1) == 1 {
					<-hctx.Done()
					close(firstEnded)
					return hctx.Err()
				}
				return nil
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, "work")).To(Succeed())
			Eventually(calls.Load, 5*time.Second).Should(BeEquivalentTo(1))

			kill(db, "replica-a")
			Eventually(firstEnded, 5*time.Second).Should(BeClosed())
			live(db, "replica-a") // its heartbeat writes the row again

			// Without the release the row stays claimed by a live id for ever: the reap
			// only frees the claims of ids that are gone.
			Eventually(calls.Load, 15*time.Second).Should(BeEquivalentTo(2))
			Eventually(rows, 5*time.Second).Should(BeZero())
		})
	})

	Describe("a replica that restarts with the same instance id", func() {
		It("recovers the job that the earlier process held, inside the liveness window", func() {
			started := make(chan struct{})
			hold := make(chan struct{})
			var ranAgain atomic.Int32

			// The first process claims the job and is gone: nothing settles the row.
			first := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			sub, err := first.Consume(ctx, messaging.WorkMCPCI, 0, func(context.Context, []byte, messaging.Publisher) error {
				close(started)
				<-hold
				return ErrKeepClaim // as a process that died would leave it
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = sub.Unsubscribe() })
			DeferCleanup(func() { close(hold) }) // runs before the Unsubscribe above, which waits for the handler
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, "work")).To(Succeed())
			Eventually(started, 5*time.Second).Should(BeClosed())

			// The new process has the same id, and the id never left the instances table.
			second := newConsumer(db, consumerOptions{owner: "replica-a", hints: bus, interval: 100 * time.Millisecond})
			consume(second, messaging.WorkMCPCI, func(context.Context, []byte, messaging.Publisher) error {
				ranAgain.Add(1)
				return nil
			})
			Eventually(ranAgain.Load, 10*time.Second).Should(BeEquivalentTo(1))
			Eventually(rows, 5*time.Second).Should(BeZero())
		})
	})

	Describe("a row that can never be run", func() {
		It("ends after the failures that the consumer allows, records the failure and deletes the row", func() {
			skipWaits()
			var calls atomic.Int32
			var gaveUp atomic.Pointer[string]
			live(db, "replica-a")
			c, err := NewClaimConsumer(ClaimConsumerConfig{
				DB: db, Owner: "replica-a", Events: bus, Hints: bus, Interval: 50 * time.Millisecond,
				MaxFailures: 3,
				OnExhausted: func(_ context.Context, _ messaging.WorkKind, payload []byte, cause error) error {
					msg := string(payload) + ": " + cause.Error()
					gaveUp.Store(&msg)
					return nil
				},
			})
			Expect(err).ToNot(HaveOccurred())
			consume(c, messaging.WorkMCPCI, func(context.Context, []byte, messaging.Publisher) error {
				calls.Add(1)
				return errors.New("the worker refused the payload")
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, "poison")).To(Succeed())

			Eventually(rows, 15*time.Second).Should(BeZero())
			Expect(calls.Load()).To(BeEquivalentTo(3), "it ran as often as the consumer allows and not again")
			Expect(*gaveUp.Load()).To(ContainSubstring("the worker refused the payload"))
		})

		It("keeps the row when the failure cannot be recorded, and tries again", func() {
			skipWaits()
			var recorded atomic.Int32
			live(db, "replica-a")
			c, err := NewClaimConsumer(ClaimConsumerConfig{
				DB: db, Owner: "replica-a", Events: bus, Interval: 50 * time.Millisecond, MaxFailures: 1,
				OnExhausted: func(context.Context, messaging.WorkKind, []byte, error) error {
					if recorded.Add(1) == 1 {
						return errors.New("the database is away")
					}
					return nil
				},
			})
			Expect(err).ToNot(HaveOccurred())
			consume(c, messaging.WorkMCPCI, func(context.Context, []byte, messaging.Publisher) error {
				return errors.New("failed")
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, "poison")).To(Succeed())
			Eventually(rows, 15*time.Second).Should(BeZero())
			Expect(recorded.Load()).To(BeNumerically(">=", 2))
		})

		It("does not count a release for lack of a worker as a failure", func() {
			skipWaits()
			var calls atomic.Int32
			live(db, "replica-a")
			c, err := NewClaimConsumer(ClaimConsumerConfig{
				DB: db, Owner: "replica-a", Events: bus, Interval: 50 * time.Millisecond, MaxFailures: 2,
			})
			Expect(err).ToNot(HaveOccurred())
			consume(c, messaging.WorkMCPCI, func(context.Context, []byte, messaging.Publisher) error {
				calls.Add(1)
				return noAnswerYet(errors.New("no agent worker is connected"))
			})
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, "waits")).To(Succeed())
			Eventually(calls.Load, 15*time.Second).Should(BeNumerically(">=", 5))
			Expect(rows()).To(BeEquivalentTo(1), "a fleet that is down keeps its queued work")
		})
	})
})
