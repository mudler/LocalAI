package upgraderun_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/mudler/LocalAI/core/services/upgraderun"
)

var _ = Describe("The outcome of a run of one item", func() {
	run := Run{ID: "run-1", State: RunRunning}

	It("is started when the item has a job", func() {
		out := SingleOutcome(run, Item{Key: "gallery:a", Phase: PhaseRunning, JobID: "job-1"})
		Expect(out.State).To(Equal(SingleStarted))
		Expect(out.JobID).To(Equal("job-1"))
		Expect(out.Err()).NotTo(HaveOccurred())
	})

	It("is refused when the item ended without a job, with its key and error", func() {
		out := SingleOutcome(run, Item{Key: "gallery:a", Phase: PhaseFailed, ErrorKey: "download", Error: "registry down"})
		Expect(out.State).To(Equal(SingleRefused))
		Expect(out.ErrorKey).To(Equal("download"))
		Expect(out.Error).To(Equal("registry down"))
		var refused *RefusedError
		Expect(errors.As(out.Err(), &refused)).To(BeTrue())
		Expect(refused.ErrorKey).To(Equal("download"))
	})

	It("gives the unknown key to a refusal without one", func() {
		out := SingleOutcome(run, Item{Key: "gallery:a", Phase: PhaseSkipped})
		Expect(out.State).To(Equal(SingleRefused))
		Expect(out.ErrorKey).To(Equal(ErrKeyUnknown))
	})

	DescribeTable("is pending while the item has no job and did not end",
		func(phase Phase) {
			out := SingleOutcome(run, Item{Key: "gallery:a", Phase: phase})
			Expect(out.State).To(Equal(SinglePending))
			Expect(out.JobID).To(BeEmpty())
			Expect(out.RunID).To(Equal("run-1"))
			var pending *PendingError
			Expect(errors.As(out.Err(), &pending)).To(BeTrue())
			Expect(pending.RunID).To(Equal("run-1"))
			Expect(out.Err().Error()).NotTo(ContainSubstring("started"))
		},
		Entry("queued", PhaseQueued),
		Entry("running, not yet accepted", PhaseRunning),
	)

	It("answers pending, not started, when StartOne waits out before the operation is accepted", func() {
		h := newHarness(harnessOpts{})
		h.backends.add(Candidate{Kind: KindGallery, Name: "slow"})
		gate := make(chan struct{})
		DeferCleanup(func() { close(gate) })
		h.starter.block(gate)
		ctx := context.Background()
		run, item, err := h.svc.StartOne(ctx, "admin", si(KindGallery, "slow"), SourceManual, 100*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
		out := SingleOutcome(run, item)
		Expect(out.State).To(Equal(SinglePending))
		Expect(out.RunID).To(Equal(run.ID))
	})
})
