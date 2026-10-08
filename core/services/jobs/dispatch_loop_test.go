package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

var _ = Describe("The dispatch loop", func() {
	var (
		ctx       context.Context
		db        *gorm.DB
		bus       *testutil.FakeBus
		picker    *fakePicker
		control   *fakeControl
		broadcast *fakeBroadcast
		store     *fakeStore
	)

	newLoop := func(owner string) *DispatchLoop {
		GinkgoHelper()
		live(db, owner)
		loop, err := NewDispatchLoop(DispatchConfig{
			DB: db, Owner: owner, Picker: picker, Control: control, Broadcast: broadcast, Store: store, Hints: bus,
			Interval: 50 * time.Millisecond,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(loop.Start(ctx)).To(Succeed())
		DeferCleanup(loop.Stop)
		return loop
	}

	rows := func() int64 {
		var n int64
		Expect(db.Model(&WorkClaim{}).Count(&n).Error).To(Succeed())
		return n
	}

	ci := func(jobID string) JobEvent { return JobEvent{JobID: jobID, TaskID: "t1"} }

	BeforeEach(func() {
		c, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ctx = c
		db = newClaimDB()
		bus = testutil.NewFakeBus()
		picker = &fakePicker{nodes: []string{"w1"}, types: map[string]string{"w1": "agent"}}
		control = &fakeControl{do: func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
			return workerctl.RunReply{}, nil
		}}
		broadcast = &fakeBroadcast{}
		store = &fakeStore{}
	})

	It("takes a unit of each kind from the queue, drives it on a worker and removes the claim", func() {
		control.do = func(_ context.Context, _, verb string, _ func(string, json.RawMessage)) (workerctl.RunReply, error) {
			if verb == workerctl.VerbMCPCIRun {
				return workerctl.RunReply{JobID: "j1", Status: "completed", Result: "ok"}, nil
			}
			return workerctl.RunReply{}, nil
		}
		newLoop("replica-a")
		queue := NewClaimQueue(db, bus)
		Expect(queue.Enqueue(ctx, messaging.WorkMCPCI, ci("j1"))).To(Succeed())
		Expect(queue.Enqueue(ctx, messaging.WorkAgentRun, map[string]string{"agent_name": "a"})).To(Succeed())
		Expect(queue.Enqueue(ctx, messaging.WorkTask, ci("j2"))).To(Succeed())

		Eventually(rows, 10*time.Second).Should(BeZero())
		verbs := map[string]bool{}
		for _, c := range control.seen() {
			verbs[c.verb] = true
		}
		Expect(verbs).To(Equal(map[string]bool{workerctl.VerbMCPCIRun: true, workerctl.VerbAgentExecute: true}))
		Expect(store.terminals()).To(Equal([]terminal{{"j1", "completed", "ok", ""}}))
	})

	It("releases the claim, with a wait, when the worker could not be reached", func() {
		control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
			return workerctl.RunReply{}, errors.New("the tunnel broke")
		}
		newLoop("replica-a")
		Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, ci("j1"))).To(Succeed())

		Eventually(func() int {
			var row WorkClaim
			if err := db.First(&row).Error; err != nil {
				return -1
			}
			return row.Attempts
		}, 10*time.Second).Should(Equal(1))
		var row WorkClaim
		Expect(db.First(&row).Error).To(Succeed())
		Expect(row.State).To(Equal(ClaimPending))
		Expect(row.NotBefore).ToNot(BeNil())
		Expect(store.terminals()).To(BeEmpty())
	})

	It("leaves the claim with its replica when the job cannot be written, and does not run the work a second time", func() {
		store.fail = errors.New("database is away")
		control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
			return workerctl.RunReply{JobID: "j1", Status: "completed"}, nil
		}
		newLoop("replica-a")
		Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, ci("j1"))).To(Succeed())

		Eventually(func() int { return len(control.seen()) }, 10*time.Second).Should(Equal(1))
		Consistently(func() int { return len(control.seen()) }, time.Second).Should(Equal(1))
		var row WorkClaim
		Expect(db.First(&row).Error).To(Succeed())
		Expect(row.State).To(Equal(ClaimClaimed))
		Expect(row.ClaimedBy).To(Equal("replica-a"))
	})

	Describe("when the carrier of the loop is released", func() {
		// run starts a loop whose context the spec can end with a cause, and waits
		// until a run is in flight on it.
		run := func() (cancel context.CancelCauseFunc, loop *DispatchLoop) {
			GinkgoHelper()
			started := make(chan struct{}, 1)
			control.do = func(ctx context.Context, _, _ string, _ func(string, json.RawMessage)) (workerctl.RunReply, error) {
				started <- struct{}{}
				<-ctx.Done()
				return workerctl.RunReply{}, ctx.Err()
			}
			live(db, "replica-a")
			loopCtx, cancel := context.WithCancelCause(ctx)
			var err error
			loop, err = NewDispatchLoop(DispatchConfig{
				DB: db, Owner: "replica-a", Picker: picker, Control: control, Broadcast: broadcast, Store: store, Hints: bus,
				Interval: 50 * time.Millisecond,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(loop.Start(loopCtx)).To(Succeed())
			Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, ci("j1"))).To(Succeed())
			Eventually(started, 10*time.Second).Should(Receive())
			return cancel, loop
		}

		It("completes the claim of a run that the end of the drain cut off, and leaves its job to the reaper, so the run is not offered again", func() {
			cancel, loop := run()
			cancel(messaging.ErrCarrierReleased)
			loop.Stop()

			Expect(rows()).To(BeZero(), "the run did start, and the carrier that took over must not start it again")
			Expect(store.terminals()).To(BeEmpty(), "the reaper decides about the job")
			Expect(control.seen()).To(HaveLen(1))
		})

		It("still releases the claim of a run that ends for another reason, such as the stop of the replica", func() {
			cancel, loop := run()
			cancel(context.Canceled)
			loop.Stop()

			var row WorkClaim
			Expect(db.First(&row).Error).To(Succeed())
			Expect(row.State).To(Equal(ClaimPending))
			Expect(row.Attempts).To(Equal(1))
		})
	})

	It("ends the run of a job that is cancelled, closes the job as cancelled and removes the claim", func() {
		started := make(chan struct{})
		control.do = func(ctx context.Context, _, _ string, _ func(string, json.RawMessage)) (workerctl.RunReply, error) {
			close(started)
			<-ctx.Done()
			return workerctl.RunReply{}, ctx.Err()
		}
		newLoop("replica-a")
		Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkMCPCI, ci("j1"))).To(Succeed())
		Eventually(started, 10*time.Second).Should(BeClosed())

		Expect(bus.Publish(messaging.SubjectJobCancel("j1"), CancelEvent{JobID: "j1"})).To(Succeed())

		Eventually(rows, 10*time.Second).Should(BeZero())
		Expect(store.terminals()).To(Equal([]terminal{{"j1", "cancelled", "", "cancelled"}}))
	})

	It("is built only with what it needs", func() {
		good := DispatchConfig{DB: db, Owner: "replica-a", Picker: picker, Control: control, Hints: bus}
		for name, mutate := range map[string]func(*DispatchConfig){
			"no database": func(c *DispatchConfig) { c.DB = nil },
			"no owner":    func(c *DispatchConfig) { c.Owner = "" },
			"no picker":   func(c *DispatchConfig) { c.Picker = nil },
			"no control":  func(c *DispatchConfig) { c.Control = nil },
			"no bus":      func(c *DispatchConfig) { c.Hints = nil },
		} {
			cfg := good
			mutate(&cfg)
			_, err := NewDispatchLoop(cfg)
			Expect(err).To(HaveOccurred(), name)
		}
		_, err := NewDispatchLoop(good)
		Expect(err).ToNot(HaveOccurred())
	})

	It("stops claiming on Stop, and waits for the runs in flight to be settled", func() {
		started := make(chan struct{})
		release := make(chan struct{})
		control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
			close(started)
			<-release
			return workerctl.RunReply{}, nil
		}
		loop := newLoop("replica-a")
		Expect(NewClaimQueue(db, bus).Enqueue(ctx, messaging.WorkAgentRun, "x")).To(Succeed())
		Eventually(started, 10*time.Second).Should(BeClosed())

		stopped := make(chan struct{})
		go func() { defer close(stopped); loop.Stop() }()
		Consistently(stopped, 200*time.Millisecond).ShouldNot(BeClosed())
		close(release)
		Eventually(stopped, 5*time.Second).Should(BeClosed())
		Expect(rows()).To(BeZero())
	})
})
