// Package jobstest is the contract suite every jobs.Store passes.
//
//nolint:forbidigo // reusable contract suite driven by the caller's *testing.T
package jobstest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
)

// NewStore returns an empty store. Run calls it once per subtest.
type NewStore func(t *testing.T) jobs.Store

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

// Run checks the store.
func Run(t *testing.T, newStore NewStore) {
	ctx := context.Background()

	t.Run("TaskRoundTripAndReplace", func(t *testing.T) {
		s := newStore(t)
		a := task("t1", "u1", "Nightly")
		if err := s.PutTask(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTask(ctx, "u1", "t1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Nightly" || got.Prompt != "Say {{.word}}" || !got.Enabled || len(got.Webhooks) != 1 ||
			got.Webhooks[0].Headers["Authorization"] != "Bearer x" || !got.Created.Equal(base) {
			t.Fatalf("got %+v", got)
		}
		a.Name, a.Webhooks, a.Created = "Nightly 2", nil, time.Time{}
		a.Updated = base.Add(time.Hour)
		if err := s.PutTask(ctx, a); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetTask(ctx, "u1", "t1")
		if got.Name != "Nightly 2" || len(got.Webhooks) != 0 || !got.Created.Equal(base) {
			t.Fatalf("after replace: %+v", got)
		}
	})

	t.Run("TasksBelongToTheirOwner", func(t *testing.T) {
		s := newStore(t)
		_ = s.PutTask(ctx, task("t1", "u1", "A"))
		if _, err := s.GetTask(ctx, "u2", "t1"); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("another owner read it: %v", err)
		}
		if err := s.PutTask(ctx, task("t1", "u2", "B")); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("another owner replaced it: %v", err)
		}
		if err := s.DeleteTask(ctx, "u2", "t1"); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("another owner deleted it: %v", err)
		}
		if err := s.DeleteTask(ctx, "u1", "t1"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteTask(ctx, "u1", "t1"); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("delete twice: %v", err)
		}
	})

	t.Run("NamesAreUniquePerOwner", func(t *testing.T) {
		s := newStore(t)
		if err := s.PutTask(ctx, task("t1", "u1", "Nightly")); err != nil {
			t.Fatal(err)
		}
		if err := s.PutTask(ctx, task("t2", "u1", " nightly ")); !errors.Is(err, jobs.ErrNameTaken) {
			t.Fatalf("same name, same owner: %v", err)
		}
		if err := s.PutTask(ctx, task("t3", "u2", "Nightly")); err != nil {
			t.Fatalf("same name, other owner: %v", err)
		}
	})

	t.Run("ListTasksSortedByName", func(t *testing.T) {
		s := newStore(t)
		for i, n := range []string{"beta", "Alpha", "gamma"} {
			_ = s.PutTask(ctx, task(string(rune('a'+i)), "u1", n))
		}
		_ = s.PutTask(ctx, task("z", "u2", "Other"))
		l, err := s.ListTasks(ctx, "u1")
		if err != nil || len(l) != 3 || l[0].Name != "Alpha" || l[1].Name != "beta" || l[2].Name != "gamma" {
			t.Fatalf("list = %+v, %v", l, err)
		}
		all, err := s.AllTasks(ctx)
		if err != nil || len(all) != 4 || all[3].Owner != "u2" {
			t.Fatalf("all = %+v, %v", all, err)
		}
	})

	t.Run("JobsRoundTripAndUpdate", func(t *testing.T) {
		s := newStore(t)
		j := job("j1", "u1", "t1", 0)
		if err := s.PutJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetJob(ctx, "u1", "j1")
		if err != nil || got.Parameters["word"] != "hi" || got.TaskID != "t1" || got.Attachments != 2 || got.WebhooksDone {
			t.Fatalf("got %+v, %v", got, err)
		}
		j.Ended, j.Error = base.Add(30*time.Second), "model failed"
		j.WebhooksDone, j.WebhookSent, j.WebhookSentAt, j.WebhookError = true, true, base.Add(time.Minute), "one failed"
		if err := s.PutJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetJob(ctx, "u1", "j1")
		if !got.WebhooksDone || !got.WebhookSent || !got.WebhookSentAt.Equal(base.Add(time.Minute)) || got.WebhookError != "one failed" ||
			!got.Ended.Equal(base.Add(30*time.Second)) || got.Error != "model failed" {
			t.Fatalf("after update: %+v", got)
		}
		if _, err := s.GetJob(ctx, "u2", "j1"); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("another owner read the job: %v", err)
		}
		if err := s.DeleteJob(ctx, "u2", "j1"); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("another owner deleted the job: %v", err)
		}
		if err := s.DeleteJob(ctx, "u1", "j1"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ListJobsNewestFirstFilteredAndLimited", func(t *testing.T) {
		s := newStore(t)
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
		if got := ids(l); got != "new mid old " {
			t.Fatalf("list = %q", got)
		}
		l, _ = s.ListJobs(ctx, "u1", jobs.Filter{TaskID: "t1"})
		if got := ids(l); got != "new old " {
			t.Fatalf("by task = %q", got)
		}
		l, _ = s.ListJobs(ctx, "u1", jobs.Filter{Limit: 1})
		if got := ids(l); got != "new " {
			t.Fatalf("limit = %q", got)
		}
		all, _ := s.AllJobs(ctx)
		if got := ids(all); got != "other new mid old " {
			t.Fatalf("all = %q", got)
		}
	})

	t.Run("DeletingATaskKeepsItsJobs", func(t *testing.T) {
		s := newStore(t)
		_ = s.PutTask(ctx, task("t1", "u1", "A"))
		_ = s.PutJob(ctx, job("j1", "u1", "t1", 0))
		if err := s.DeleteTask(ctx, "u1", "t1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetJob(ctx, "u1", "j1"); err != nil {
			t.Fatalf("the job went with its task: %v", err)
		}
	})
}
