package studio_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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

func newJobRig(t *testing.T, script func(workspace.Actor, string) []studiotest.ToolStep, mod ...func(*studio.JobOptions)) *jobRig {
	t.Helper()
	svc, _ := newToolService(t, script)
	st, err := jobs.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	r := &jobRig{svc: svc, hooks: &fakeWebhooks{}, store: st, now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	o := studio.JobOptions{Store: st, Webhooks: r.hooks, Backoff: []time.Duration{time.Millisecond, time.Millisecond}, Clock: func() time.Time { return r.now },
		AllowPrivate: func() bool { return false }}
	for _, m := range mod {
		m(&o)
	}
	r.host, err = studio.NewJobs(svc, o)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetJobs(r.host)
	t.Cleanup(r.host.Close)
	return r
}

func (r *jobRig) task(t *testing.T, in studio.TaskInput) jobs.Task {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (r *jobRig) wait(t *testing.T, user, id string) studio.JobInfo {
	t.Helper()
	var info studio.JobInfo
	until(t, "the job to end", func() bool {
		var err error
		if info, err = r.host.GetJob(context.Background(), user, id); err != nil {
			t.Fatal(err)
		}
		return info.Status.Final() && !info.Ended.IsZero()
	})
	return info
}

func TestAJobIsABackgroundChatOfItsOwner(t *testing.T) {
	r := newJobRig(t, saySummary)
	task := r.task(t, studio.TaskInput{})
	job, err := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{Params: map[string]string{"topic": "the week"}})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != studio.JobPending && job.Status != studio.JobRunning {
		t.Fatalf("a new job is %q", job.Status)
	}
	info := r.wait(t, alice, job.ID)
	if info.Status != studio.JobCompleted || info.Result != "All caught up." || info.TaskName != "Summary" || info.TriggeredBy != "api" {
		t.Fatalf("job = %+v", info)
	}
	chat, err := r.svc.Get(context.Background(), alice, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Origin != session.OriginJob || chat.OriginID != task.ID || chat.Model != "m" {
		t.Fatalf("the job chat = %+v, want origin job of the task", chat)
	}
	msgs, _ := r.svc.Messages(context.Background(), alice, job.ID, 0, 100)
	if len(msgs) == 0 || msgs[0].Role != session.RoleUser || msgs[0].Text != "Summarise the week" {
		t.Fatalf("the prompt was not the rendered template: %+v", msgs)
	}
}

func TestAJobBelongsToItsOwner(t *testing.T) {
	r := newJobRig(t, saySummary)
	task := r.task(t, studio.TaskInput{Prompt: "Hi"})
	job, err := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	r.wait(t, alice, job.ID)
	ctx := context.Background()
	if _, err := r.host.GetJob(ctx, "bob", job.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("bob read the job: %v", err)
	}
	if _, err := r.host.Execute(ctx, "bob", task.ID, studio.RunRequest{}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("bob ran alice's task: %v", err)
	}
	if err := r.host.DeleteJob(ctx, "bob", job.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("bob deleted the job: %v", err)
	}
	if l, _ := r.host.ListJobs(ctx, "bob", studio.JobQuery{}); len(l) != 0 {
		t.Fatalf("bob lists %d jobs", len(l))
	}
	if all, err := r.host.AdminJobs(ctx, studio.JobQuery{}); err != nil || len(all) != 1 || all[0].Owner != alice {
		t.Fatalf("the admin view = %+v, %v", all, err)
	}
}

func TestAMissingParameterAndABadAttachmentAreRefusedBeforeAnythingRuns(t *testing.T) {
	r := newJobRig(t, saySummary)
	task := r.task(t, studio.TaskInput{})
	ctx := context.Background()
	if _, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{}); !errors.Is(err, studio.ErrBadJobInput) || !strings.Contains(err.Error(), "parameter") {
		t.Fatalf("missing parameter: %v", err)
	}
	params := map[string]string{"topic": "x"}
	for name, att := range map[string]studio.Attachment{
		"an address":   {Kind: "image", Data: "https://example.test/a.png"},
		"bad base64":   {Kind: "file", Data: "data:text/plain;base64,@@@"},
		"not base64":   {Kind: "file", Data: "data:text/plain,hello"},
		"empty string": {Kind: "audio", Data: ""},
	} {
		if _, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{Params: params, Attachments: []studio.Attachment{att}}); !errors.Is(err, studio.ErrBadJobInput) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if chats, _ := r.svc.List(ctx, alice, 100); len(chats) != 0 {
		t.Fatalf("a refused job left %d chats", len(chats))
	}
	ok := studio.Attachment{Kind: "file", Data: "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("hello"))}
	if _, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{Params: params, Attachments: []studio.Attachment{ok}}); err != nil && !strings.Contains(err.Error(), "files") {
		// With the files off a good attachment fails at the send, with the reason.
		t.Fatalf("a data URI was refused: %v", err)
	}
}

func TestADisabledTaskDoesNotRun(t *testing.T) {
	r := newJobRig(t, saySummary)
	off := false
	task := r.task(t, studio.TaskInput{Enabled: &off, Prompt: "Hi"})
	if _, err := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{}); !errors.Is(err, studio.ErrTaskDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestAJobThatFailsHasTheReason(t *testing.T) {
	r := newJobRig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Err: errors.New("the model is gone")}}
	})
	task := r.task(t, studio.TaskInput{Prompt: "Hi"})
	job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
	info := r.wait(t, alice, job.ID)
	if info.Status != studio.JobFailed || info.Error == "" {
		t.Fatalf("job = %+v", info)
	}
}

func TestACancelledJobStopsItsTurn(t *testing.T) {
	block := make(chan struct{})
	r := newJobRig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Block: block}}
	})
	task := r.task(t, studio.TaskInput{Prompt: "Hi"})
	ctx := context.Background()
	job, _ := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
	until(t, "the job to run", func() bool {
		i, _ := r.host.GetJob(ctx, alice, job.ID)
		return i.Status == studio.JobRunning
	})
	if err := r.host.CancelJob(ctx, alice, job.ID); err != nil {
		t.Fatal(err)
	}
	info := r.wait(t, alice, job.ID)
	if info.Status != studio.JobCancelled {
		t.Fatalf("status = %q, want cancelled", info.Status)
	}
	if err := r.host.CancelJob(ctx, alice, job.ID); !errors.Is(err, studio.ErrJobEnded) {
		t.Fatalf("cancel twice: %v", err)
	}
	close(block)
}

func TestAJobThatRunsTooLongIsStopped(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	r := newJobRig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Block: block}}
	}, func(o *studio.JobOptions) { o.RunLimit = 100 * time.Millisecond })
	task := r.task(t, studio.TaskInput{Prompt: "Hi"})
	job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
	info := r.wait(t, alice, job.ID)
	if info.Status != studio.JobCancelled || !strings.Contains(info.Error, "took longer than") {
		t.Fatalf("job = %+v", info)
	}
}

func TestWebhooksAreCalledOnceWhenAJobEnds(t *testing.T) {
	r := newJobRig(t, saySummary)
	task := r.task(t, studio.TaskInput{Prompt: "Hi", Webhooks: []jobs.Webhook{
		{URL: "https://hooks.example.test/a", Headers: map[string]string{"Authorization": "Bearer secret"}},
		{URL: "https://hooks.example.test/b", Method: "PUT", PayloadTemplate: `{"task":"{{.Task.Name}}","status":"{{.Status}}","answer":{{.Result | json}}}`},
	}})
	r.hooks.Fail = 1 // the first attempt of the first webhook fails and is retried
	job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
	info := r.wait(t, alice, job.ID)
	until(t, "the webhooks to be recorded", func() bool {
		i, _ := r.host.GetJob(context.Background(), alice, job.ID)
		info = i
		return i.WebhooksDone
	})
	if !info.WebhookSent || info.WebhookError != "" {
		t.Fatalf("webhook state = sent %v, error %q", info.WebhookSent, info.WebhookError)
	}
	calls := r.hooks.all()
	if len(calls) != 3 { // a twice (one retry), b once
		t.Fatalf("%d calls: %+v", len(calls), calls)
	}
	a, b := calls[0], calls[len(calls)-1]
	if a.Method != "POST" || a.Headers["Authorization"] != "Bearer secret" || !strings.Contains(string(a.Body), `"status":"completed"`) || !strings.Contains(string(a.Body), `"job_id":"`+job.ID+`"`) {
		t.Fatalf("default webhook = %s %v %s", a.Method, a.Headers, a.Body)
	}
	if b.Method != "PUT" || string(b.Body) != `{"task":"Summary","status":"completed","answer":"All caught up."}` {
		t.Fatalf("templated webhook = %s %s", b.Method, b.Body)
	}
}

func TestAWebhookThatKeepsFailingIsRecordedOnTheJob(t *testing.T) {
	r := newJobRig(t, saySummary)
	task := r.task(t, studio.TaskInput{Prompt: "Hi", Webhooks: []jobs.Webhook{{URL: "https://hooks.example.test/a"}}})
	r.hooks.Fail = 100
	job, _ := r.host.Execute(context.Background(), alice, task.ID, studio.RunRequest{})
	var info studio.JobInfo
	until(t, "the webhooks to be recorded", func() bool {
		info, _ = r.host.GetJob(context.Background(), alice, job.ID)
		return info.WebhooksDone
	})
	if info.WebhookSent || !strings.Contains(info.WebhookError, "all webhooks failed") {
		t.Fatalf("job = %+v", info)
	}
	if info.Status != studio.JobCompleted {
		t.Fatalf("a failed webhook changed the job status to %q", info.Status)
	}
}

func TestATaskRefusesAWebhookToAnInternalAddress(t *testing.T) {
	r := newJobRig(t, saySummary)
	_, err := r.host.CreateTask(context.Background(), alice, studio.TaskInput{Name: "x", Model: "m", Prompt: "p",
		Webhooks: []jobs.Webhook{{URL: "http://127.0.0.1:9/hook"}}})
	if !errors.Is(err, jobs.ErrInvalid) || !strings.Contains(err.Error(), "private") {
		t.Fatalf("err = %v", err)
	}
	r2 := newJobRig(t, saySummary, func(o *studio.JobOptions) { o.AllowPrivate = func() bool { return true } })
	if _, err := r2.host.CreateTask(context.Background(), alice, studio.TaskInput{Name: "x", Model: "m", Prompt: "p",
		Webhooks: []jobs.Webhook{{URL: "http://127.0.0.1:9/hook"}}}); err != nil {
		t.Fatalf("an administrator allowed private addresses: %v", err)
	}
}

func TestUpdatingATaskKeepsHiddenHeaderValues(t *testing.T) {
	r := newJobRig(t, saySummary)
	task := r.task(t, studio.TaskInput{Prompt: "Hi", Webhooks: []jobs.Webhook{{URL: "https://hooks.example.test/a", Headers: map[string]string{"Authorization": "Bearer secret", "X-Old": "gone"}}}})
	_, err := r.host.UpdateTask(context.Background(), alice, task.ID, studio.TaskInput{Name: "Summary", Model: "m", Prompt: "Hi",
		Webhooks: []jobs.Webhook{{URL: "https://hooks.example.test/a", Headers: map[string]string{"Authorization": studio.HiddenHeader, "X-New": "v"}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := r.host.GetTask(context.Background(), alice, task.ID)
	h := got.Webhooks[0].Headers
	if h["Authorization"] != "Bearer secret" || h["X-New"] != "v" || len(h) != 2 {
		t.Fatalf("headers = %v", h)
	}
}

func TestTheSweepRemovesOldJobsAndFinishesOrphans(t *testing.T) {
	r := newJobRig(t, saySummary, func(o *studio.JobOptions) { o.RetentionDays = 7 })
	task := r.task(t, studio.TaskInput{Prompt: "Hi"})
	ctx := context.Background()
	job, _ := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
	r.wait(t, alice, job.ID)
	if err := r.host.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.host.GetJob(ctx, alice, job.ID); err != nil {
		t.Fatalf("a fresh job was removed: %v", err)
	}
	r.now = r.now.AddDate(0, 0, 8)
	if err := r.host.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.host.GetJob(ctx, alice, job.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("an old job stayed: %v", err)
	}
	if _, err := r.svc.Get(ctx, alice, job.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("the chat of an old job stayed: %v", err)
	}

	// A job whose watcher died: the record has no end. The sweep records it.
	orphan, _ := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{})
	r.wait(t, alice, orphan.ID)
	rec, _ := r.store.GetJob(ctx, alice, orphan.ID)
	rec.Ended, rec.Error, rec.WebhooksDone = time.Time{}, "", false
	if err := r.store.PutJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := r.host.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.store.GetJob(ctx, alice, orphan.ID); got.Ended.IsZero() || !got.WebhooksDone {
		t.Fatalf("the sweep did not finish the orphan: %+v", got)
	}
}

func TestRerunUsesTheSameTaskAndParameters(t *testing.T) {
	r := newJobRig(t, saySummary)
	task := r.task(t, studio.TaskInput{})
	ctx := context.Background()
	first, err := r.host.Execute(ctx, alice, task.ID, studio.RunRequest{Params: map[string]string{"topic": "A"}})
	if err != nil {
		t.Fatal(err)
	}
	r.wait(t, alice, first.ID)
	again, err := r.host.Rerun(ctx, alice, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	info := r.wait(t, alice, again.ID)
	if again.ID == first.ID || info.TaskID != task.ID || info.Parameters["topic"] != "A" || info.TriggeredBy != "rerun" {
		t.Fatalf("rerun = %+v", info)
	}
}

func TestTracesListTheReasoningAndToolResults(t *testing.T) {
	var chats store.Store
	svc, _ := newToolService(t, saySummary, func(d *studio.Deps) { chats = d.Store })
	st, err := jobs.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	host, err := studio.NewJobs(svc, studio.JobOptions{Store: st, Webhooks: &fakeWebhooks{}})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetJobs(host)
	t.Cleanup(host.Close)
	ctx := context.Background()
	task, err := host.CreateTask(ctx, alice, studio.TaskInput{Name: "T", Model: "m", Prompt: "Hi"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := host.Execute(ctx, alice, task.ID, studio.RunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the job to end", func() bool {
		i, _ := host.GetJob(ctx, alice, job.ID)
		return i.Status.Final()
	})
	for _, e := range []session.Entry{{Role: session.RoleReasoning, Text: "think"}, {Role: session.RoleTool, Text: "search: found"}} {
		if _, err := chats.AppendEntry(ctx, job.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := host.Traces(ctx, alice, job.ID)
	if err != nil || len(got) != 2 || got[0].Type != "reasoning" || got[1].Type != "tool_result" || got[1].Content != "search: found" {
		t.Fatalf("traces = %+v, %v", got, err)
	}
	if _, err := host.Traces(ctx, "bob", job.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("bob read the traces: %v", err)
	}
}
