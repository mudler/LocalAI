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

	It("recognizes a missing load record through the sentinel", func() {
		err := fmt.Errorf("no load info for model m: %w (cause: %w)", ErrNoLoadInfo, errors.New("record not found"))
		Expect(errors.Is(err, ErrNoLoadInfo)).To(BeTrue())
	})
})
