package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ReplicaReconciler scale-up backoff", func() {
	var (
		now       time.Time
		scheduler *fakeScheduler
		rc        *ReplicaReconciler
		cfg       ModelSchedulingConfig
	)

	BeforeEach(func() {
		now = time.Unix(1_700_000_000, 0)
		scheduler = &fakeScheduler{scheduleErr: errors.New("load boom")}
		rc = NewReplicaReconciler(ReplicaReconcilerOptions{Scheduler: scheduler})
		rc.now = func() time.Time { return now }
		cfg = ModelSchedulingConfig{ModelName: "m"}
	})

	It("skips the scheduler while a failed model is backing off", func() {
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(1))

		now = now.Add(scaleUpBackoffBase - time.Second)
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(1), "no schedule call inside the backoff window")

		now = now.Add(2 * time.Second)
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(2), "retry once the window has passed")
	})

	It("doubles the delay on each failure up to the cap", func() {
		Expect(scaleUpBackoffDelay(1)).To(Equal(scaleUpBackoffBase))
		Expect(scaleUpBackoffDelay(2)).To(Equal(2 * scaleUpBackoffBase))
		Expect(scaleUpBackoffDelay(3)).To(Equal(4 * scaleUpBackoffBase))
		Expect(scaleUpBackoffDelay(50)).To(Equal(scaleUpBackoffMax))
		Expect(scaleUpBackoffDelay(1 << 30)).To(Equal(scaleUpBackoffMax))
	})

	It("forgets the failures after a successful scale-up", func() {
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		now = now.Add(scaleUpBackoffMax)
		scheduler.scheduleErr = nil
		scheduler.scheduleNode = &BackendNode{Name: "n"}
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeTrue())

		scheduler.scheduleErr = errors.New("load boom")
		scheduler.scheduleNode = nil
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		// The failure counter restarted, so the window is the base delay.
		now = now.Add(scaleUpBackoffBase + time.Second)
		before := len(scheduler.scheduleCalls)
		rc.scaleUp(context.Background(), cfg, 1)
		Expect(scheduler.scheduleCalls).To(HaveLen(before + 1))
	})

	It("backs off each model on its own", func() {
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		other := ModelSchedulingConfig{ModelName: "other"}
		Expect(rc.scaleUp(context.Background(), other, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(2))
	})

	It("does not count a load that another owner runs as a failure", func() {
		// The model's load job is held by another attempt, for example a slow
		// request-path load. That is not a failed scale-up: backing off would
		// keep the reconciler away for up to ten minutes after that load ends
		// or fails.
		scheduler.scheduleErr = fmt.Errorf("model m is already being loaded by another owner: %w", ErrLoadInProgress)
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(2), "the next tick retries at once")

		scheduler.scheduleErr = errors.New("load boom")
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		now = now.Add(scaleUpBackoffBase + time.Second)
		before := len(scheduler.scheduleCalls)
		rc.scaleUp(context.Background(), cfg, 1)
		Expect(scheduler.scheduleCalls).To(HaveLen(before+1), "the real failure is the first one, so the window is the base delay")
	})

	It("does not grow the backoff of a model when its load is in progress", func() {
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		now = now.Add(scaleUpBackoffBase + time.Second)
		scheduler.scheduleErr = fmt.Errorf("wrapped: %w", ErrLoadInProgress)
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(rc.scaleUpFailures[cfg.ModelName].count).To(Equal(1))
	})

	It("forgets the backoff when a node becomes schedulable", func() {
		rc.observeSchedulableNodes([]string{"node-a"})
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(1), "backing off")

		rc.observeSchedulableNodes([]string{"node-a"})
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(1), "nothing changed, still backing off")

		// A node registers, is approved, recovers or resumes: it joins the set.
		rc.observeSchedulableNodes([]string{"node-a", "node-b"})
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(2), "a new node is a reason to try again at once")
	})

	It("forgets the backoff when a node that left comes back", func() {
		rc.observeSchedulableNodes([]string{"node-a", "node-b"})
		rc.observeSchedulableNodes([]string{"node-a"}) // node-b drained or went offline
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(1))

		rc.observeSchedulableNodes([]string{"node-a", "node-b"}) // node-b resumed
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(2))
	})

	It("does not forget the backoff when a node leaves", func() {
		rc.observeSchedulableNodes([]string{"node-a", "node-b"})
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		rc.observeSchedulableNodes([]string{"node-a"})
		Expect(rc.scaleUp(context.Background(), cfg, 1)).To(BeFalse())
		Expect(scheduler.scheduleCalls).To(HaveLen(1))
	})

	It("recognizes a missing load record through the sentinel", func() {
		err := fmt.Errorf("no load info for model m: %w (cause: %w)", ErrNoLoadInfo, errors.New("record not found"))
		Expect(errors.Is(err, ErrNoLoadInfo)).To(BeTrue())
	})
})
