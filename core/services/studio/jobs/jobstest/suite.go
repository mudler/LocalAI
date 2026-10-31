// Package jobstest is the contract suite every jobs.Store passes. Run
// registers Ginkgo specs, so call it inside a Describe of the implementation's
// test package.
package jobstest

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
)

// NewStore returns an empty store. Each spec calls it once; clean up with
// DeferCleanup.
type NewStore func() jobs.Store

var base = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func task(id, owner, name string) jobs.Task {
	return jobs.Task{ID: id, Owner: owner, Name: name, Description: "d", Model: "m1", Prompt: "Say {{.word}}", Enabled: true,
		Webhooks: []jobs.Webhook{{URL: "https://example.test/hook", Method: "POST", Headers: map[string]string{"Authorization": "Bearer x"}}},
		Created:  base, Updated: base}
}

func job(id, owner, taskID string, age time.Duration) jobs.Job {
	return jobs.Job{ID: id, Owner: owner, TaskID: taskID, Parameters: map[string]string{"word": "hi"}, TriggeredBy: "api",
		Attachments: 2, Created: base.Add(age)}
}

// Run registers the specs that check the store.
func Run(newStore NewStore) {
	ctx := context.Background()

	It("TaskRoundTripAndReplace", func() {
		s := newStore()
		a := task("t1", "u1", "Nightly")
		Expect(s.PutTask(ctx, a)).To(Succeed())
		got, err := s.GetTask(ctx, "u1", "t1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Nightly"))
		Expect(got.Prompt).To(Equal("Say {{.word}}"))
		Expect(got.Enabled).To(BeTrue())
		Expect(got.Webhooks).To(HaveLen(1))
		Expect(got.Webhooks[0].Headers["Authorization"]).To(Equal("Bearer x"))
		Expect(got.Created.Equal(base)).To(BeTrue(), "got %+v", got)
		a.Name, a.Webhooks, a.Created = "Nightly 2", nil, time.Time{}
		a.Updated = base.Add(time.Hour)
		Expect(s.PutTask(ctx, a)).To(Succeed())
		got, _ = s.GetTask(ctx, "u1", "t1")
		Expect(got.Name).To(Equal("Nightly 2"))
		Expect(got.Webhooks).To(BeEmpty())
		Expect(got.Created.Equal(base)).To(BeTrue(), "after replace: %+v", got)
	})

	It("TasksBelongToTheirOwner", func() {
		s := newStore()
		_ = s.PutTask(ctx, task("t1", "u1", "A"))
		_, err := s.GetTask(ctx, "u2", "t1")
		Expect(err).To(MatchError(jobs.ErrNotFound), "another owner read it")
		Expect(s.PutTask(ctx, task("t1", "u2", "B"))).To(MatchError(jobs.ErrNotFound), "another owner replaced it")
		Expect(s.DeleteTask(ctx, "u2", "t1")).To(MatchError(jobs.ErrNotFound), "another owner deleted it")
		Expect(s.DeleteTask(ctx, "u1", "t1")).To(Succeed())
		Expect(s.DeleteTask(ctx, "u1", "t1")).To(MatchError(jobs.ErrNotFound), "delete twice")
	})

	It("NamesAreUniquePerOwner", func() {
		s := newStore()
		Expect(s.PutTask(ctx, task("t1", "u1", "Nightly"))).To(Succeed())
		Expect(s.PutTask(ctx, task("t2", "u1", " nightly "))).To(MatchError(jobs.ErrNameTaken), "same name, same owner")
		Expect(s.PutTask(ctx, task("t3", "u2", "Nightly"))).To(Succeed(), "same name, other owner")
	})

	It("ListTasksSortedByName", func() {
		s := newStore()
		for i, n := range []string{"beta", "Alpha", "gamma"} {
			_ = s.PutTask(ctx, task(string(rune('a'+i)), "u1", n))
		}
		_ = s.PutTask(ctx, task("z", "u2", "Other"))
		l, err := s.ListTasks(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(HaveLen(3))
		Expect(l[0].Name).To(Equal("Alpha"))
		Expect(l[1].Name).To(Equal("beta"))
		Expect(l[2].Name).To(Equal("gamma"))
		all, err := s.AllTasks(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(4))
		Expect(all[3].Owner).To(Equal("u2"))
	})

	It("JobsRoundTripAndUpdate", func() {
		s := newStore()
		j := job("j1", "u1", "t1", 0)
		Expect(s.PutJob(ctx, j)).To(Succeed())
		got, err := s.GetJob(ctx, "u1", "j1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Parameters["word"]).To(Equal("hi"))
		Expect(got.TaskID).To(Equal("t1"))
		Expect(got.Attachments).To(Equal(2))
		Expect(got.WebhooksDone).To(BeFalse())
		j.Ended, j.Error = base.Add(30*time.Second), "model failed"
		j.WebhooksDone, j.WebhookSent, j.WebhookSentAt, j.WebhookError = true, true, base.Add(time.Minute), "one failed"
		Expect(s.PutJob(ctx, j)).To(Succeed())
		got, _ = s.GetJob(ctx, "u1", "j1")
		Expect(got.WebhooksDone).To(BeTrue(), "after update: %+v", got)
		Expect(got.WebhookSent).To(BeTrue())
		Expect(got.WebhookSentAt.Equal(base.Add(time.Minute))).To(BeTrue())
		Expect(got.WebhookError).To(Equal("one failed"))
		Expect(got.Ended.Equal(base.Add(30 * time.Second))).To(BeTrue())
		Expect(got.Error).To(Equal("model failed"))
		_, err = s.GetJob(ctx, "u2", "j1")
		Expect(err).To(MatchError(jobs.ErrNotFound), "another owner read the job")
		Expect(s.DeleteJob(ctx, "u2", "j1")).To(MatchError(jobs.ErrNotFound), "another owner deleted the job")
		Expect(s.DeleteJob(ctx, "u1", "j1")).To(Succeed())
	})

	It("ListJobsNewestFirstFilteredAndLimited", func() {
		s := newStore()
		_ = s.PutJob(ctx, job("old", "u1", "t1", 0))
		_ = s.PutJob(ctx, job("mid", "u1", "t2", time.Hour))
		_ = s.PutJob(ctx, job("new", "u1", "t1", 2*time.Hour))
		_ = s.PutJob(ctx, job("other", "u2", "t1", 3*time.Hour))
		ids := func(l []jobs.Job) string {
			out := ""
			for _, j := range l {
				out += j.ID + " "
			}
			return out
		}
		l, _ := s.ListJobs(ctx, "u1", jobs.Filter{})
		Expect(ids(l)).To(Equal("new mid old "), "list")
		l, _ = s.ListJobs(ctx, "u1", jobs.Filter{TaskID: "t1"})
		Expect(ids(l)).To(Equal("new old "), "by task")
		l, _ = s.ListJobs(ctx, "u1", jobs.Filter{Limit: 1})
		Expect(ids(l)).To(Equal("new "), "limit")
		all, _ := s.AllJobs(ctx)
		Expect(ids(all)).To(Equal("other new mid old "), "all")
	})

	It("DeletingATaskKeepsItsJobs", func() {
		s := newStore()
		_ = s.PutTask(ctx, task("t1", "u1", "A"))
		_ = s.PutJob(ctx, job("j1", "u1", "t1", 0))
		Expect(s.DeleteTask(ctx, "u1", "t1")).To(Succeed())
		_, err := s.GetJob(ctx, "u1", "j1")
		Expect(err).NotTo(HaveOccurred(), "the job went with its task")
	})
}
