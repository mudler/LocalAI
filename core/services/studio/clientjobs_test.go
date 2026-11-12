package studio_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// slowPut delays the write of a job record.
type slowPut struct{ jobs.Store }

func (s slowPut) PutJob(ctx context.Context, j jobs.Job) error {
	time.Sleep(30 * time.Millisecond)
	return s.Store.PutJob(ctx, j)
}

var _ = Describe("jobs of a paired client", func() {
	ctx := context.Background()

	It("starts a job for a client, which only that client can read or cancel", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		job, err := r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(job.ClientID).To(Equal("cl-1"))
		Expect(job.TriggeredBy).To(Equal("client"))

		got, err := r.host.GetClientJob(ctx, alice, "cl-1", job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(job.ID))

		_, err = r.host.GetClientJob(ctx, alice, "cl-2", job.ID)
		Expect(err).To(MatchError(jobs.ErrNotFound))
		Expect(r.host.CancelClientJob(ctx, alice, "cl-2", job.ID)).To(MatchError(jobs.ErrNotFound))
		_, err = r.host.GetClientJob(ctx, alice, "", job.ID)
		Expect(err).To(MatchError(jobs.ErrNotFound))
		_, err = r.host.GetClientJob(ctx, bob, "cl-1", job.ID)
		Expect(err).To(HaveOccurred())

		// A job that no client started is not a client's job.
		own, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.GetClientJob(ctx, alice, "cl-1", own.ID)
		Expect(err).To(MatchError(jobs.ErrNotFound))
		r.wait(alice, job.ID)
		r.wait(alice, own.ID)
	})

	It("refuses a job without a client and limits the running jobs of one client", func() {
		block := make(chan struct{})
		r := newJobRig(func(_ workspace.Actor, _ string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}, {Say: "done"}}
		})
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		_, err := r.host.ExecuteForClient(ctx, alice, "", task.ID, studio.RunRequest{})
		Expect(err).To(MatchError(studio.ErrBadJobInput))

		var ids []string
		for range studio.MaxRunningClientJobs {
			j, err := r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
			Expect(err).NotTo(HaveOccurred())
			ids = append(ids, j.ID)
		}
		_, err = r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
		Expect(err).To(MatchError(studio.ErrTooManyClientJobs))
		// Another client is not held back by it.
		other, err := r.host.ExecuteForClient(ctx, alice, "cl-2", task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		ids = append(ids, other.ID)

		close(block)
		for _, id := range ids {
			r.wait(alice, id)
		}
	})

	It("keeps a rerun of a client job a job of that client, with the same read-only rule", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		first, err := r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		r.wait(alice, first.ID)
		again, err := r.host.Rerun(ctx, alice, first.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ClientID).To(Equal("cl-1"))
		Expect(again.TriggeredBy).To(Equal("rerun"))
		r.wait(alice, again.ID)
		_, err = r.host.GetClientJob(ctx, alice, "cl-2", again.ID)
		Expect(err).To(MatchError(jobs.ErrNotFound))

		// A rerun of an ordinary job stays an ordinary job.
		own, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		r.wait(alice, own.ID)
		own2, err := r.host.Rerun(ctx, alice, own.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(own2.ClientID).To(BeEmpty())
		r.wait(alice, own2.ID)
	})

	It("lets a client that has many finished jobs start another one", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		for i := range 250 {
			done := r.now.Add(-time.Hour)
			Expect(r.store.PutJob(ctx, jobs.Job{ID: fmt.Sprintf("old-%d", i), Owner: alice, TaskID: task.ID, ClientID: "cl-1",
				Created: done.Add(-time.Minute), Ended: done})).To(Succeed())
		}
		j, err := r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		r.wait(alice, j.ID)
	})

	It("keeps the stored records of a client under a cap and removes the oldest ended ones", func() {
		r := newJobRig(saySummary, func(o *studio.JobOptions) { o.MaxClientRecords = 5 })
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		ended := func(id, client string, age time.Duration) {
			done := r.now.Add(-age)
			Expect(r.store.PutJob(ctx, jobs.Job{ID: id, Owner: alice, TaskID: task.ID, ClientID: client, Created: done.Add(-time.Minute), Ended: done})).To(Succeed())
		}
		for i := range 8 {
			ended(fmt.Sprintf("old-%d", i), "cl-1", time.Duration(8-i)*time.Hour) // old-0 is the oldest
		}
		ended("other", "cl-2", 20*time.Hour)
		ended("mine", "", 20*time.Hour)

		j, err := r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred(), "the cap trims, it does not refuse")
		r.wait(alice, j.ID)
		kept, err := r.store.ListJobs(ctx, alice, jobs.Filter{ClientID: "cl-1", Finished: true, OldestFirst: true})
		Expect(err).NotTo(HaveOccurred())
		ids := []string{}
		for _, k := range kept {
			ids = append(ids, k.ID)
		}
		// Room was made for the new job: four of the old ones stay, the new one
		// is the fifth.
		Expect(ids).To(Equal([]string{"old-4", "old-5", "old-6", "old-7", j.ID}))
		_, err = r.store.GetJob(ctx, alice, "other")
		Expect(err).NotTo(HaveOccurred(), "another client is untouched")
		_, err = r.store.GetJob(ctx, alice, "mine")
		Expect(err).NotTo(HaveOccurred(), "a job that no client started is untouched")
	})

	It("holds the quota when a client starts eight jobs at once", func() {
		block := make(chan struct{})
		// A slow store widens the window between the check and the record.
		r := newJobRig(func(_ workspace.Actor, _ string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}, {Say: "done"}}
		}, func(o *studio.JobOptions) { o.Store = slowPut{o.Store} })
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		var mu sync.Mutex
		var ids []string
		refused := 0
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				j, err := r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					Expect(err).To(MatchError(studio.ErrTooManyClientJobs))
					refused++
					return
				}
				ids = append(ids, j.ID)
			}()
		}
		wg.Wait()
		Expect(ids).To(HaveLen(studio.MaxRunningClientJobs))
		Expect(refused).To(Equal(8 - studio.MaxRunningClientJobs))
		close(block)
		for _, id := range ids {
			r.wait(alice, id)
		}
	})

	It("stops the running jobs of a client that is revoked, and only its own", func() {
		block := make(chan struct{})
		r := newJobRig(func(_ workspace.Actor, _ string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}, {Say: "done"}}
		})
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		mine, err := r.host.ExecuteForClient(ctx, alice, "cl-1", task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		other, err := r.host.ExecuteForClient(ctx, alice, "cl-2", task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		n, err := r.host.CancelClientJobs(ctx, alice, "cl-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		Expect(r.wait(alice, mine.ID).Status).To(Equal(studio.JobCancelled))
		got, err := r.host.GetClientJob(ctx, alice, "cl-2", other.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status.Final()).To(BeFalse(), "the job of another client runs on")
		close(block)
		r.wait(alice, other.ID)
	})
})
