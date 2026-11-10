package studio_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

var _ = Describe("the person's view of the jobs of a chat", func() {
	startOne := func() (jobChat, string, string) {
		c := newJobChat("on", nil)
		id := create(c.svc, alice)
		c.say(alice, id, "start build make all")
		stubs := c.stubs(alice, id)
		Expect(stubs).To(HaveLen(1))
		return c, id, stubs[0].ID
	}

	It("lists a running job with its live numbers and never its command", func() {
		c, id, job := startOne()
		c.host.write(job, "compiling\n")
		list, err := c.svc.BgJobs(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(job))
		Expect(list[0].Label).To(Equal("build"))
		Expect(list[0].State).To(Equal(bgjob.StateRunning))
		Expect(list[0].Total).To(BeNumerically("==", len("compiling\n")))
		Expect(list[0].LastOutput.IsZero()).To(BeFalse())
		Expect(strings.Join([]string{list[0].Label, list[0].Provider}, " ")).NotTo(ContainSubstring("make"))
	})

	It("stops a job as the person: the end reason is stopped", func() {
		c, id, job := startOne()
		_, err := c.svc.StopJob(context.Background(), alice, id, job)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bgjob.EndReason {
			list, _ := c.svc.BgJobs(context.Background(), alice, id)
			return list[0].End
		}).Should(Equal(bgjob.EndStopped))
		_, err = c.svc.StopJob(context.Background(), alice, id, job)
		Expect(err).To(MatchError(studio.ErrBgJobEnded))
	})

	It("refuses an unknown job and the chat of another user", func() {
		c, id, job := startOne()
		_, err := c.svc.StopJob(context.Background(), alice, id, "job-nope")
		Expect(err).To(MatchError(studio.ErrBgJobNotFound))
		_, err = c.svc.StopJob(context.Background(), bob, id, job)
		Expect(err).To(HaveOccurred())
		Expect(c.host.proc(job).code).To(BeZero())
	})
})
