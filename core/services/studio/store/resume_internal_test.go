package store

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("resume limits", func() {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	ginkgo.DescribeTable("resumeOpen",
		func(attempts int, interruptedAt, notBefore time.Time, wantOpen, wantDue bool) {
			open, due := resumeOpen(attempts, interruptedAt, notBefore, now)
			Expect(open).To(Equal(wantOpen))
			Expect(due).To(Equal(wantDue))
		},
		ginkgo.Entry("none scheduled", 0, now, time.Time{}, false, false),
		ginkgo.Entry("waiting", 0, now.Add(-5*time.Second), now.Add(10*time.Second), true, false),
		ginkgo.Entry("due", 1, now.Add(-time.Minute), now.Add(-time.Second), true, true),
		ginkgo.Entry("tries used", MaxResumeAttempts, now.Add(-time.Minute), now.Add(-time.Second), false, false),
		ginkgo.Entry("window passed", 0, now.Add(-ResumeWindow-time.Second), now.Add(-time.Second), false, false),
		ginkgo.Entry("inside the window", 0, now.Add(-ResumeWindow+time.Second), now.Add(-time.Second), true, true),
	)

	ginkgo.It("waits 15 s, 60 s, then 5 min, and clamps", func() {
		Expect(ResumeBackoff(0)).To(Equal(15 * time.Second))
		Expect(ResumeBackoff(1)).To(Equal(time.Minute))
		Expect(ResumeBackoff(2)).To(Equal(5 * time.Minute))
		Expect(ResumeBackoff(9)).To(Equal(5 * time.Minute))
		Expect(ResumeBackoff(-1)).To(Equal(15 * time.Second))
	})
})
