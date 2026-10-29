package studio_test

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeWebhooks records the calls and fails the first Fail of them.
type fakeWebhooks struct {
	mu    sync.Mutex
	calls []studio.WebhookCall
	Fail  int
}

func (f *fakeWebhooks) Send(_ context.Context, c studio.WebhookCall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if f.Fail > 0 {
		f.Fail--
		return errors.New("the server answered 500")
	}
	return nil
}

func (f *fakeWebhooks) all() []studio.WebhookCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]studio.WebhookCall(nil), f.calls...)
}

type jobRig struct {
	svc   *studio.Service
	host  *studio.Jobs
	hooks *fakeWebhooks
	store jobs.Store
	now   time.Time
}

func newJobRig(script func(workspace.Actor, string) []studiotest.ToolStep, mod ...func(*studio.JobOptions)) *jobRig {
	svc, _ := newToolService(script)
	st, err := jobs.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r := &jobRig{svc: svc, hooks: &fakeWebhooks{}, store: st, now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	o := studio.JobOptions{Store: st, Webhooks: r.hooks, Backoff: []time.Duration{time.Millisecond, time.Millisecond}, Clock: func() time.Time { return r.now },
		AllowPrivate: func() bool { return false }}
	for _, m := range mod {
		m(&o)
	}
	r.host, err = studio.NewJobs(svc, o)
	Expect(err).NotTo(HaveOccurred())
	svc.SetJobs(r.host)
	DeferCleanup(r.host.Close)
	return r
}

func (r *jobRig) task(in studio.TaskInput) jobs.Task {
	if in.Name == "" {
		in.Name = "Summary"
	}
	if in.Model == "" {
		in.Model = "m"
	}
	if in.Prompt == "" {
		in.Prompt = "Summarise {{.topic}}"
	}
	task, err := r.host.CreateTask(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	return task
}

func (r *jobRig) wait(user, id string) studio.JobInfo {
	var info studio.JobInfo
	until("the job to end", func() bool {
		var err error
		info, err = r.host.GetJob(context.Background(), user, id)
		Expect(err).NotTo(HaveOccurred())
		return info.Status.Final() && !info.Ended.IsZero()
	})
	return info
}

var _ = Describe("jobsvc", func() {
	It("a job is a background chat of its owner", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{})
		job, err := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{Params: map[string]string{"topic": "the week"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(job.Status).To(BeElementOf(studio.JobPending, studio.JobRunning), "a new job is pending or running")
		info := r.wait(alice, job.ID)
		Expect(info.Status).To(Equal(studio.JobCompleted), "job = %+v", info)
		Expect(info.Result).To(Equal("All caught up."), "job = %+v", info)
		Expect(info.TaskName).To(Equal("Summary"), "job = %+v", info)
		Expect(info.TriggeredBy).To(Equal("api"), "job = %+v", info)
		chat, err := r.svc.Get(context.Background(), alice, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.Origin).To(Equal(session.OriginJob), "the job chat = %+v, want origin job of the task", chat)
		Expect(chat.OriginID).To(Equal(task.ID), "the job chat = %+v, want origin job of the task", chat)
		Expect(chat.Model).To(Equal("m"), "the job chat = %+v, want origin job of the task", chat)
		msgs, _ := r.svc.Messages(context.Background(), alice, job.ID, 0, 100)
		Expect(msgs).NotTo(BeEmpty(), "the prompt was not the rendered template: %+v", msgs)
		Expect(msgs[0].Role).To(Equal(session.RoleUser), "the prompt was not the rendered template: %+v", msgs)
		Expect(msgs[0].Text).To(Equal("Summarise the week"), "the prompt was not the rendered template: %+v", msgs)
	})

	It("a job belongs to its owner", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		job, err := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		r.wait(alice, job.ID)
		ctx := context.Background()
		_, err = r.host.GetJob(ctx, "bob", job.ID)
		Expect(err).To(MatchError(jobs.ErrNotFound), "bob read the job: %v", err)
		_, err = r.host.Execute(ctx, "bob", task.ID, studio.RunRequest{})
		Expect(err).To(MatchError(jobs.ErrNotFound), "bob ran alice's task: %v", err)
		Expect(r.host.DeleteJob(ctx, "bob", job.ID)).To(MatchError(jobs.ErrNotFound))
		l, _ := r.host.ListJobs(ctx, "bob", studio.JobQuery{})
		Expect(l).To(BeEmpty(), "bob lists %d jobs", len(l))
		all, err := r.host.AdminJobs(ctx, studio.JobQuery{})
		Expect(err).NotTo(HaveOccurred(), "the admin view = %+v, %v", all, err)
		Expect(all).To(HaveLen(1), "the admin view = %+v, %v", all, err)
		Expect(all[0].Owner).To(Equal(alice), "the admin view = %+v, %v", all, err)
	})

	It("a missing parameter and a bad attachment are refused before anything runs", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{})
		ctx := context.Background()
		_, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
		Expect(err).To(MatchError(studio.ErrBadJobInput), "missing parameter: %v", err)
		Expect(err.Error()).To(ContainSubstring("parameter"), "missing parameter: %v", err)
		params := map[string]string{"topic": "x"}
		for name, att := range map[string]studio.Attachment{
			"an address":   {Kind: "image", Data: "https://example.test/a.png"},
			"bad base64":   {Kind: "file", Data: "data:text/plain;base64,@@@"},
			"not base64":   {Kind: "file", Data: "data:text/plain,hello"},
			"empty string": {Kind: "audio", Data: ""},
		} {
			_, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{Params: params, Attachments: []studio.Attachment{att}})
			Expect(err).To(MatchError(studio.ErrBadJobInput), "%s: %v", name, err)
		}
		chats, _ := r.svc.List(ctx, alice, 100)
		Expect(chats).To(BeEmpty(), "a refused job left %d chats", len(chats))
		ok := studio.Attachment{Kind: "file", Data: "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("hello"))}
		if _, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{Params: params, Attachments: []studio.Attachment{ok}}); err != nil {
			// With the files off a good attachment fails at the send, with the reason.
			Expect(err.Error()).To(ContainSubstring("files"), "a data URI was refused")
		}
	})

	It("a disabled task does not run", func() {
		r := newJobRig(saySummary)
		off := false
		task := r.task(studio.TaskInput{Enabled: &off, Prompt: "Hi"})
		_, err := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
		Expect(err).To(MatchError(studio.ErrTaskDisabled), "err = %v", err)
	})

	It("a job that fails has the reason", func() {
		r := newJobRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Err: errors.New("the model is gone")}}
		})
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
		info := r.wait(alice, job.ID)
		Expect(info.Status).To(Equal(studio.JobFailed), "job = %+v", info)
		Expect(info.Error).NotTo(Equal(""), "job = %+v", info)
	})

	It("a cancelled job stops its turn", func() {
		block := make(chan struct{})
		r := newJobRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}}
		})
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		ctx := context.Background()
		job, _ := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
		until("the job to run", func() bool {
			i, _ := r.host.GetJob(ctx, alice, job.ID)
			return i.Status == studio.JobRunning
		})
		Expect(r.host.CancelJob(ctx, alice, job.ID)).To(Succeed())
		info := r.wait(alice, job.ID)
		Expect(info.Status).To(Equal(studio.JobCancelled), "status = %q, want cancelled", info.Status)
		Expect(r.host.CancelJob(ctx, alice, job.ID)).To(MatchError(studio.ErrJobEnded))
		close(block)
	})

	It("a job that runs too long is stopped", func() {
		block := make(chan struct{})
		defer close(block)
		r := newJobRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}}
		}, func(o *studio.JobOptions) { o.RunLimit = 100 * time.Millisecond })
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
		info := r.wait(alice, job.ID)
		Expect(info.Status).To(Equal(studio.JobCancelled), "job = %+v", info)
		Expect(info.Error).To(ContainSubstring("took longer than"), "job = %+v", info)
	})

	It("webhooks are called once when a job ends", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{Prompt: "Hi", Webhooks: []jobs.Webhook{
			{URL: "https://hooks.example.test/a", Headers: map[string]string{"Authorization": "Bearer secret"}},
			{URL: "https://hooks.example.test/b", Method: "PUT", PayloadTemplate: `{"task":"{{.Task.Name}}","status":"{{.Status}}","answer":{{.Result | json}}}`},
		}})
		r.hooks.Fail = 1 // the first attempt of the first webhook fails and is retried
		job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
		info := r.wait(alice, job.ID)
		until("the webhooks to be recorded", func() bool {
			i, _ := r.host.GetJob(context.Background(), alice, job.ID)
			info = i
			return i.WebhooksDone
		})
		Expect(info.WebhookSent).To(BeTrue(), "webhook state = sent %v, error %q", info.WebhookSent, info.WebhookError)
		Expect(info.WebhookError).To(Equal(""), "webhook state = sent %v, error %q", info.WebhookSent, info.WebhookError)
		calls := r.hooks.all()
		Expect(calls).To(HaveLen(3), "a twice (one retry), b once")
		a, b := calls[0], calls[len(calls)-1]
		Expect(a.Method).To(Equal("POST"), "default webhook = %s %v %s", a.Method, a.Headers, a.Body)
		Expect(a.Headers["Authorization"]).To(Equal("Bearer secret"), "default webhook = %s %v %s", a.Method, a.Headers, a.Body)
		Expect(string(a.Body)).To(ContainSubstring(`"status":"completed"`), "default webhook = %s %v %s", a.Method, a.Headers, a.Body)
		Expect(string(a.Body)).To(ContainSubstring(`"job_id":"`+job.ID+`"`), "default webhook = %s %v %s", a.Method, a.Headers, a.Body)
		Expect(b.Method).To(Equal("PUT"), "templated webhook = %s %s", b.Method, b.Body)
		Expect(string(b.Body)).To(Equal(`{"task":"Summary","status":"completed","answer":"All caught up."}`), "templated webhook = %s %s", b.Method, b.Body)
	})

	It("a webhook that keeps failing is recorded on the job", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{Prompt: "Hi", Webhooks: []jobs.Webhook{{URL: "https://hooks.example.test/a"}}})
		r.hooks.Fail = 100
		job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
		var info studio.JobInfo
		until("the webhooks to be recorded", func() bool {
			info, _ = r.host.GetJob(context.Background(), alice, job.ID)
			return info.WebhooksDone
		})
		Expect(info.WebhookSent).To(BeFalse(), "job = %+v", info)
		Expect(info.WebhookError).To(ContainSubstring("all webhooks failed"), "job = %+v", info)
		Expect(info.Status).To(Equal(studio.JobCompleted), "a failed webhook changed the job status to %q", info.Status)
	})

	It("a task refuses a webhook to an internal address", func() {
		r := newJobRig(saySummary)
		_, err := r.host.CreateTask(context.Background(), alice, studio.TaskInput{Name: "x", Model: "m", Prompt: "p",
			Webhooks: []jobs.Webhook{{URL: "http://127.0.0.1:9/hook"}}})
		Expect(err).To(MatchError(jobs.ErrInvalid), "err = %v", err)
		Expect(err.Error()).To(ContainSubstring("private"), "err = %v", err)
		r2 := newJobRig(saySummary, func(o *studio.JobOptions) { o.AllowPrivate = func() bool { return true } })
		_, err = r2.host.CreateTask(context.Background(), alice, studio.TaskInput{Name: "x", Model: "m", Prompt: "p",
			Webhooks: []jobs.Webhook{{URL: "http://127.0.0.1:9/hook"}}})
		Expect(err).NotTo(HaveOccurred(), "an administrator allowed private addresses")
	})

	It("updating a task keeps hidden header values", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{Prompt: "Hi", Webhooks: []jobs.Webhook{{URL: "https://hooks.example.test/a", Headers: map[string]string{"Authorization": "Bearer secret", "X-Old": "gone"}}}})
		_, err := r.host.UpdateTask(context.Background(), alice, task.ID, studio.TaskInput{Name: "Summary", Model: "m", Prompt: "Hi",
			Webhooks: []jobs.Webhook{{URL: "https://hooks.example.test/a", Headers: map[string]string{"Authorization": studio.HiddenHeader, "X-New": "v"}}}})
		Expect(err).NotTo(HaveOccurred())
		got, _ := r.host.GetTask(context.Background(), alice, task.ID)
		h := got.Webhooks[0].Headers
		Expect(h["Authorization"]).To(Equal("Bearer secret"), "headers = %v", h)
		Expect(h["X-New"]).To(Equal("v"), "headers = %v", h)
		Expect(h).To(HaveLen(2), "headers = %v", h)
	})

	It("the sweep removes old jobs and finishes orphans", func() {
		r := newJobRig(saySummary, func(o *studio.JobOptions) { o.RetentionDays = 7 })
		task := r.task(studio.TaskInput{Prompt: "Hi"})
		ctx := context.Background()
		job, _ := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
		r.wait(alice, job.ID)
		Expect(r.host.Sweep(ctx)).To(Succeed())
		_, err := r.host.GetJob(ctx, alice, job.ID)
		Expect(err).NotTo(HaveOccurred(), "a fresh job was removed")
		r.now = r.now.AddDate(0, 0, 8)
		Expect(r.host.Sweep(ctx)).To(Succeed())
		_, err = r.host.GetJob(ctx, alice, job.ID)
		Expect(err).To(MatchError(jobs.ErrNotFound), "an old job stayed: %v", err)
		_, err = r.svc.Get(ctx, alice, job.ID)
		Expect(err).To(MatchError(session.ErrNotFound), "the chat of an old job stayed: %v", err)

		// A job whose watcher died: the record has no end. The sweep records it.
		orphan, _ := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
		r.wait(alice, orphan.ID)
		rec, _ := r.store.GetJob(ctx, alice, orphan.ID)
		rec.Ended, rec.Error, rec.WebhooksDone = time.Time{}, "", false
		Expect(r.store.PutJob(ctx, rec)).To(Succeed())
		Expect(r.host.Sweep(ctx)).To(Succeed())
		got, _ := r.store.GetJob(ctx, alice, orphan.ID)
		Expect(got.Ended.IsZero()).To(BeFalse(), "the sweep did not finish the orphan: %+v", got)
		Expect(got.WebhooksDone).To(BeTrue(), "the sweep did not finish the orphan: %+v", got)
	})

	It("rerun uses the same task and parameters", func() {
		r := newJobRig(saySummary)
		task := r.task(studio.TaskInput{})
		ctx := context.Background()
		first, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{Params: map[string]string{"topic": "A"}})
		Expect(err).NotTo(HaveOccurred())
		r.wait(alice, first.ID)
		again, err := r.host.Rerun(ctx, alice, first.ID)
		Expect(err).NotTo(HaveOccurred())
		info := r.wait(alice, again.ID)
		Expect(again.ID).NotTo(Equal(first.ID), "rerun = %+v", info)
		Expect(info.TaskID).To(Equal(task.ID), "rerun = %+v", info)
		Expect(info.Parameters["topic"]).To(Equal("A"), "rerun = %+v", info)
		Expect(info.TriggeredBy).To(Equal("rerun"), "rerun = %+v", info)
	})

	It("traces list the reasoning and tool results", func() {
		var chats store.Store
		svc, _ := newToolService(saySummary, func(d *studio.Deps) { chats = d.Store })
		st, err := jobs.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewJobs(svc, studio.JobOptions{Store: st, Webhooks: &fakeWebhooks{}})
		Expect(err).NotTo(HaveOccurred())
		svc.SetJobs(host)
		DeferCleanup(host.Close)
		ctx := context.Background()
		task, err := host.CreateTask(ctx, alice, studio.TaskInput{Name: "T", Model: "m", Prompt: "Hi"})
		Expect(err).NotTo(HaveOccurred())
		job, err := host.Execute(ctx, alice, task.ID, studio.RunRequest{})
		Expect(err).NotTo(HaveOccurred())
		until("the job to end", func() bool {
			i, _ := host.GetJob(ctx, alice, job.ID)
			return i.Status.Final()
		})
		for _, e := range []session.Entry{{Role: session.RoleReasoning, Text: "think"}, {Role: session.RoleTool, Text: "search: found"}} {
			_, err := chats.AppendEntry(ctx, job.ID, e)
			Expect(err).NotTo(HaveOccurred())
		}
		got, err := host.Traces(ctx, alice, job.ID)
		Expect(err).NotTo(HaveOccurred(), "traces = %+v, %v", got, err)
		Expect(got).To(HaveLen(2), "traces = %+v, %v", got, err)
		Expect(got[0].Type).To(Equal("reasoning"), "traces = %+v, %v", got, err)
		Expect(got[1].Type).To(Equal("tool_result"), "traces = %+v, %v", got, err)
		Expect(got[1].Content).To(Equal("search: found"), "traces = %+v, %v", got, err)
		_, err = host.Traces(ctx, "bob", job.ID)
		Expect(err).To(MatchError(jobs.ErrNotFound), "bob read the traces: %v", err)
	})
})
