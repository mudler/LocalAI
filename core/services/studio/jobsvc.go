package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// Errors of the agent jobs. Service errors that the HTTP layer maps wrap one of
// these or jobs.ErrInvalid.
var (
	// ErrJobsOff means the service has no job host.
	ErrJobsOff = errors.New("studio: agent jobs are not available")
	// ErrTaskDisabled means the task is switched off.
	ErrTaskDisabled = errors.New("studio: the task is switched off")
	// ErrBadJobInput means the parameters or the attachments of a job are not usable.
	ErrBadJobInput = errors.New("studio: bad job input")
	// ErrTooManyTasks means the user has the most tasks they may have.
	ErrTooManyTasks = fmt.Errorf("studio: you can have at most %d tasks", jobs.MaxTasksPerUser)
	// ErrTooManyJobs means the user has the most jobs they may run at once.
	ErrTooManyJobs = fmt.Errorf("studio: %d jobs are running; wait for one to end", MaxRunningJobs)
	// ErrJobEnded means the job already ended and cannot be cancelled.
	ErrJobEnded = errors.New("studio: the job has already ended")
)

// Defaults and limits of the jobs.
const (
	// DefaultJobLimit is the longest a job may run.
	DefaultJobLimit = 10 * time.Minute
	// DefaultJobRetentionDays is how long ended jobs are kept.
	DefaultJobRetentionDays = 30
	// MaxRunningJobs is how many jobs one user may have running at once.
	MaxRunningJobs = 10
	// MaxAttachments is how many files one job may start with.
	MaxAttachments = 8
	// MaxAttachmentBytes is the largest decoded attachment.
	MaxAttachmentBytes = 20 << 20
	// HiddenHeader replaces the value of a webhook header in the API. An update
	// that sends it back keeps the stored value.
	HiddenHeader = "[hidden]"

	jobSweepEvery = time.Hour
	// webhookGrace is how long a job's own watcher has to deliver its webhooks
	// before the leader's sweep does it.
	webhookGrace = 5 * time.Minute
)

// JobStatus is the state of a job. It is read from the chat that runs it.
type JobStatus string

// The states of a job.
const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// Final tells whether the job has ended.
func (s JobStatus) Final() bool { return s == JobCompleted || s == JobFailed || s == JobCancelled }

// JobOptions configure the job host.
type JobOptions struct {
	// Store holds tasks and job records. Required.
	Store jobs.Store
	// RunLimit is the longest a job may run. Zero means DefaultJobLimit.
	RunLimit time.Duration
	// RetentionDays is how long ended jobs are kept. Zero means
	// DefaultJobRetentionDays.
	RetentionDays int
	// AllowPrivate says whether a webhook may call an address on a private or
	// internal network. Nil: never.
	AllowPrivate func() bool
	// Lead says whether this process runs the sweep. Nil: always (one process).
	Lead func(ctx context.Context) bool
	// Webhooks delivers a webhook. Nil: the default client, which refuses
	// internal addresses unless AllowPrivate says so. Tests replace it.
	Webhooks WebhookSender
	// Backoff is the wait before each retry of a webhook. Nil: 1, 2 and 4 seconds.
	Backoff []time.Duration
	// Clock returns the time. Nil: time.Now.
	Clock  func() time.Time
	Logger *slog.Logger
}

// Jobs runs the agent tasks. A task is a prompt template with the model or
// saved agent that runs it; a job is a chat that the same loop as every other
// chat runs, in the background, as the task's owner. The host keeps no second
// execution engine: it creates the chat, sends the rendered prompt and watches
// the turn end. The status, the result and the transcript are the chat's.
//
// The run entry point (Execute) takes the owner and the inputs and nothing from
// HTTP, so a worker can call it later.
//
// State mode: shared. Tasks and job records are in the store. A watcher runs on
// the frontend that started the job; the leader's sweep ends the bookkeeping of
// a job whose watcher died and removes jobs past their retention.
type Jobs struct {
	svc *Service
	o   JobOptions
	log *slog.Logger

	mu    sync.Mutex
	watch map[string]struct{} // jobs whose end this process watches

	wmu  sync.Mutex // serialises writes of tasks
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// NewJobs returns the job host of a service. Run starts the sweep.
func NewJobs(svc *Service, o JobOptions) (*Jobs, error) {
	if svc == nil || o.Store == nil {
		return nil, errors.New("studio: the job host needs a service and a store")
	}
	if o.RunLimit <= 0 {
		o.RunLimit = DefaultJobLimit
	}
	if o.RetentionDays <= 0 {
		o.RetentionDays = DefaultJobRetentionDays
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Backoff == nil {
		o.Backoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	}
	if o.Webhooks == nil {
		o.Webhooks = newWebhookClient(o.AllowPrivate)
	}
	return &Jobs{svc: svc, o: o, log: o.Logger, watch: map[string]struct{}{}, done: make(chan struct{})}, nil
}

// Jobs returns the job host of the service, or nil.
func (s *Service) Jobs() *Jobs { return s.jobs }

// SetJobs gives the service its job host. The application calls it once, before
// the service is shared.
func (s *Service) SetJobs(j *Jobs) { s.jobs = j }

// Run sweeps the jobs now and then until ctx ends: ended jobs past their
// retention are removed and the bookkeeping of orphaned jobs is finished.
func (j *Jobs) Run(ctx context.Context) {
	tick := time.NewTicker(jobSweepEvery)
	defer tick.Stop()
	for {
		if j.o.Lead == nil || j.o.Lead(ctx) {
			if err := j.Sweep(ctx); err != nil && ctx.Err() == nil {
				j.log.Warn("studio: could not sweep the agent jobs", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-j.done:
			return
		case <-tick.C:
		}
	}
}

// Close stops the watchers. A job that runs goes on; the next sweep finishes its
// bookkeeping.
func (j *Jobs) Close() {
	j.once.Do(func() { close(j.done) })
	j.wg.Wait()
}

// TaskInput is the definition of a task as its owner writes it.
type TaskInput struct {
	Name        string
	Description string
	Model       string
	AgentID     string
	Prompt      string
	// Enabled nil means on.
	Enabled  *bool
	Webhooks []jobs.Webhook
}

func (j *Jobs) buildTask(ctx context.Context, owner string, in TaskInput) (jobs.Task, error) {
	enabled := in.Enabled == nil || *in.Enabled
	t := jobs.Task{Owner: owner, Name: strings.TrimSpace(in.Name), Description: in.Description, Model: strings.TrimSpace(in.Model),
		AgentID: strings.TrimSpace(in.AgentID), Prompt: in.Prompt, Enabled: enabled, Webhooks: in.Webhooks}
	if err := jobs.Validate(t); err != nil {
		return jobs.Task{}, err
	}
	if t.AgentID != "" {
		ag, err := j.svc.Agent(ctx, owner, t.AgentID)
		if err != nil {
			return jobs.Task{}, err
		}
		if t.Model == "" && strings.TrimSpace(ag.Model) == "" {
			return jobs.Task{}, fmt.Errorf("%w: task model is required: the agent %q has none", jobs.ErrInvalid, ag.Name)
		}
	}
	for i, w := range t.Webhooks {
		if err := checkWebhookURL(w.URL, j.o.AllowPrivate); err != nil {
			return jobs.Task{}, fmt.Errorf("%w: webhook %d: %v", jobs.ErrInvalid, i+1, err)
		}
	}
	return t, nil
}

// CreateTask saves a task of the user.
func (j *Jobs) CreateTask(ctx context.Context, user string, in TaskInput) (jobs.Task, error) {
	owner := principal(user)
	t, err := j.buildTask(ctx, owner, in)
	if err != nil {
		return jobs.Task{}, err
	}
	j.wmu.Lock()
	defer j.wmu.Unlock()
	mine, err := j.o.Store.ListTasks(ctx, owner)
	if err != nil {
		return jobs.Task{}, err
	}
	if len(mine) >= jobs.MaxTasksPerUser {
		return jobs.Task{}, ErrTooManyTasks
	}
	t.ID = uuid.NewString()
	t.Created = j.o.Clock().UTC()
	t.Updated = t.Created
	if err := j.o.Store.PutTask(ctx, t); err != nil {
		return jobs.Task{}, err
	}
	return t, nil
}

// UpdateTask replaces a task of the user. A webhook header whose value is
// HiddenHeader keeps the value the stored webhook with the same URL has.
func (j *Jobs) UpdateTask(ctx context.Context, user, id string, in TaskInput) (jobs.Task, error) {
	owner := principal(user)
	t, err := j.buildTask(ctx, owner, in)
	if err != nil {
		return jobs.Task{}, err
	}
	j.wmu.Lock()
	defer j.wmu.Unlock()
	old, err := j.o.Store.GetTask(ctx, owner, id)
	if err != nil {
		return jobs.Task{}, err
	}
	t.ID, t.Created, t.Updated = old.ID, old.Created, j.o.Clock().UTC()
	t.Webhooks = keepHeaders(old.Webhooks, t.Webhooks)
	if err := j.o.Store.PutTask(ctx, t); err != nil {
		return jobs.Task{}, err
	}
	return t, nil
}

// keepHeaders restores the stored value of each header the caller sent back as
// HiddenHeader, matching webhooks by URL. A hidden header with no stored value
// is dropped.
func keepHeaders(old, next []jobs.Webhook) []jobs.Webhook {
	for i, w := range next {
		if len(w.Headers) == 0 {
			continue
		}
		kept := make(map[string]string, len(w.Headers))
		for k, v := range w.Headers {
			if v != HiddenHeader {
				kept[k] = v
				continue
			}
			for _, o := range old {
				if o.URL == w.URL {
					if stored, ok := o.Headers[k]; ok {
						kept[k] = stored
					}
				}
			}
		}
		next[i].Headers = kept
	}
	return next
}

// GetTask returns a task of the user.
func (j *Jobs) GetTask(ctx context.Context, user, id string) (jobs.Task, error) {
	return j.o.Store.GetTask(ctx, principal(user), id)
}

// FindTask returns the task of the user with this name, ignoring case.
func (j *Jobs) FindTask(ctx context.Context, user, name string) (jobs.Task, error) {
	mine, err := j.o.Store.ListTasks(ctx, principal(user))
	if err != nil {
		return jobs.Task{}, err
	}
	i := slices.IndexFunc(mine, func(t jobs.Task) bool { return jobs.NameKey(t.Name) == jobs.NameKey(name) })
	if i < 0 {
		return jobs.Task{}, jobs.ErrNotFound
	}
	return mine[i], nil
}

// ListTasks returns the tasks of the user sorted by name.
func (j *Jobs) ListTasks(ctx context.Context, user string) ([]jobs.Task, error) {
	return j.o.Store.ListTasks(ctx, principal(user))
}

// AdminTasks returns the tasks of every user. Only the administrator gate may
// reach it.
func (j *Jobs) AdminTasks(ctx context.Context) ([]jobs.Task, error) {
	return j.o.Store.AllTasks(ctx)
}

// DeleteTask removes a task of the user. Its jobs stay until their retention.
func (j *Jobs) DeleteTask(ctx context.Context, user, id string) error {
	j.wmu.Lock()
	defer j.wmu.Unlock()
	return j.o.Store.DeleteTask(ctx, principal(user), id)
}

// Attachment is a file a job starts with.
type Attachment struct {
	Kind string // image, video, audio or file
	Data string // a data URI: data:<mime>;base64,<data>
}

// RunRequest is what Execute needs to start a job. It has nothing from HTTP.
type RunRequest struct {
	Params      map[string]string
	Attachments []Attachment
	// TriggeredBy says where the run came from: manual, api or rerun.
	TriggeredBy string
}

// JobInfo is a job with its state read from its chat.
type JobInfo struct {
	jobs.Job
	TaskName    string
	Status      JobStatus
	Result      string
	StartedAt   time.Time
	CompletedAt time.Time
}

// renderPrompt fills the task's template with the parameters. A parameter the
// template names and the caller left out is an error, not an empty word.
func renderPrompt(prompt string, params map[string]string) (string, error) {
	tmpl, err := template.New("prompt").Option("missingkey=error").Parse(prompt)
	if err != nil {
		return "", fmt.Errorf("%w: the prompt is not a valid template: %v", jobs.ErrInvalid, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("%w: the prompt needs a parameter that was not given: %v", ErrBadJobInput, err)
	}
	return buf.String(), nil
}

// uploads decodes the attachments of a request. Only data URIs are accepted:
// the host never fetches an address for a caller.
func uploads(att []Attachment) ([]session.Upload, error) {
	if len(att) > MaxAttachments {
		return nil, fmt.Errorf("%w: a job takes at most %d attachments", ErrBadJobInput, MaxAttachments)
	}
	out := make([]session.Upload, 0, len(att))
	for i, a := range att {
		head, body, ok := strings.Cut(a.Data, ",")
		if !ok || !strings.HasPrefix(head, "data:") || !strings.HasSuffix(head, ";base64") {
			return nil, fmt.Errorf("%w: %s %d must be a base64 data URI (data:<type>;base64,<data>); addresses are not fetched", ErrBadJobInput, a.Kind, i+1)
		}
		raw, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %d is not valid base64", ErrBadJobInput, a.Kind, i+1)
		}
		if len(raw) > MaxAttachmentBytes {
			return nil, fmt.Errorf("%w: %s %d is larger than %d MB", ErrBadJobInput, a.Kind, i+1, MaxAttachmentBytes>>20)
		}
		mt := strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
		ext := ""
		if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
			ext = exts[0]
		}
		out = append(out, session.Upload{Name: fmt.Sprintf("%s-%d%s", a.Kind, i+1, ext), Body: bytes.NewReader(raw)})
	}
	return out, nil
}

// Execute starts a job of the user for a task and returns at once; the job runs
// in the background. The job is a chat of the owner with origin job.
func (j *Jobs) Execute(ctx context.Context, user, taskID string, req RunRequest) (JobInfo, error) {
	owner := principal(user)
	t, err := j.o.Store.GetTask(ctx, owner, taskID)
	if err != nil {
		return JobInfo{}, err
	}
	if !t.Enabled {
		return JobInfo{}, ErrTaskDisabled
	}
	if req.Params == nil {
		req.Params = map[string]string{}
	}
	text, err := renderPrompt(t.Prompt, req.Params)
	if err != nil {
		return JobInfo{}, err
	}
	up, err := uploads(req.Attachments)
	if err != nil {
		return JobInfo{}, err
	}
	if n, err := j.running(ctx, owner); err != nil {
		return JobInfo{}, err
	} else if n >= MaxRunningJobs {
		return JobInfo{}, ErrTooManyJobs
	}
	rec, err := j.svc.CreateAs(ctx, owner, CreateInput{Title: "Job: " + t.Name, Model: t.Model, AgentID: t.AgentID},
		Origin{Kind: session.OriginJob, ID: t.ID})
	if err != nil {
		return JobInfo{}, err
	}
	started := false
	defer func() {
		if !started {
			if err := j.svc.Delete(context.WithoutCancel(ctx), owner, rec.ID); err != nil {
				j.log.Warn("studio: could not delete the chat of a job that did not start", "chat", rec.ID, "error", err)
			}
			_ = j.o.Store.DeleteJob(context.WithoutCancel(ctx), owner, rec.ID)
		}
	}()
	by := req.TriggeredBy
	if by == "" {
		by = "api"
	}
	job := jobs.Job{ID: rec.ID, Owner: owner, TaskID: t.ID, Parameters: req.Params, TriggeredBy: by, Attachments: len(up), Created: j.o.Clock().UTC()}
	if err := j.o.Store.PutJob(ctx, job); err != nil {
		return JobInfo{}, err
	}
	events, cancel, err := j.svc.Events(ctx, owner, rec.ID) // before Send: the end must not be missed
	if err != nil {
		return JobInfo{}, err
	}
	j.mu.Lock()
	j.watch[rec.ID] = struct{}{}
	j.mu.Unlock()
	if _, err := j.svc.SendBackground(ctx, owner, rec.ID, text, up); err != nil {
		cancel()
		j.mu.Lock()
		delete(j.watch, rec.ID)
		j.mu.Unlock()
		return JobInfo{}, err
	}
	started = true
	j.wg.Add(1)
	go j.supervise(job, events, cancel) // #nosec G118 -- the job outlives the request that starts it; cancel ends it
	return JobInfo{Job: job, TaskName: t.Name, Status: JobPending}, nil
}

// running counts the jobs of the owner that have not ended.
func (j *Jobs) running(ctx context.Context, owner string) (int, error) {
	all, err := j.o.Store.ListJobs(ctx, owner, jobs.Filter{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, rec := range all {
		if !rec.Ended.IsZero() {
			continue
		}
		if info, err := j.svc.Turn(ctx, owner, rec.ID); err == nil && info.Running {
			n++
		}
	}
	return n, nil
}

// supervise waits for the end of the job's turn, records it and delivers the
// webhooks. A host that closes leaves the job to the sweep.
func (j *Jobs) supervise(job jobs.Job, events <-chan session.Event, cancel func()) {
	defer j.wg.Done()
	defer cancel()
	defer func() {
		j.mu.Lock()
		delete(j.watch, job.ID)
		j.mu.Unlock()
	}()
	end := j.svc.awaitTurn(job.Owner, job.ID, events, j.o.RunLimit, j.done)
	errText := end.ErrText
	switch end.How {
	case TurnAbandoned:
		return
	case TurnTimedOut:
		if err := j.svc.Stop(context.Background(), job.Owner, job.ID); err != nil {
			j.log.Warn("studio: could not stop a job that ran too long", "job", job.ID, "error", err)
		}
		errText = "took longer than " + j.o.RunLimit.String()
	}
	j.finish(context.Background(), job, errText)
}

// finish records the end of a job and delivers its webhooks once.
func (j *Jobs) finish(ctx context.Context, job jobs.Job, errText string) {
	cur, err := j.o.Store.GetJob(ctx, job.Owner, job.ID)
	if err != nil {
		return // deleted while it ran
	}
	if cur.Ended.IsZero() {
		cur.Ended = j.o.Clock().UTC()
		cur.Error = limitError(errText)
		if err := j.o.Store.PutJob(ctx, cur); err != nil {
			j.log.Warn("studio: could not record the end of a job", "job", job.ID, "error", err)
			return
		}
	}
	j.deliver(ctx, cur)
}

func limitError(s string) string {
	const max = 500
	r := []rune(strings.TrimSpace(s))
	if len(r) > max {
		return string(r[:max])
	}
	return string(r)
}

// info reads the state of a job from its chat.
func (j *Jobs) info(ctx context.Context, rec jobs.Job, taskName string) JobInfo {
	out := JobInfo{Job: rec, TaskName: taskName, Status: JobPending, StartedAt: rec.Created}
	turn, err := j.svc.Turn(ctx, rec.Owner, rec.ID)
	switch {
	case err != nil: // the chat is gone: nothing more is known
		out.Status = JobFailed
		if out.Error == "" {
			out.Error = "the chat of the job was deleted"
		}
	case turn.Running:
		out.Status = JobRunning
	case turn.Last == "":
		out.Status = JobPending
	case turn.Last.Finished():
		out.Status = JobCompleted
	case turn.Last == store.TurnStopped:
		out.Status = JobCancelled
	default:
		out.Status = JobFailed
		if out.Error == "" {
			out.Error = "the turn " + string(turn.Last)
		}
	}
	if out.Status.Final() {
		out.CompletedAt = rec.Ended
		if out.CompletedAt.IsZero() {
			if chat, err := j.svc.Get(ctx, rec.Owner, rec.ID); err == nil {
				out.CompletedAt = chat.Updated
			}
		}
		if err == nil {
			out.Result = j.svc.finalText(ctx, rec.ID)
		}
	}
	return out
}

func (j *Jobs) taskName(ctx context.Context, owner, id string) string {
	if t, err := j.o.Store.GetTask(ctx, owner, id); err == nil {
		return t.Name
	}
	return ""
}

// GetJob returns a job of the user.
func (j *Jobs) GetJob(ctx context.Context, user, id string) (JobInfo, error) {
	owner := principal(user)
	rec, err := j.o.Store.GetJob(ctx, owner, id)
	if err != nil {
		return JobInfo{}, err
	}
	return j.info(ctx, rec, j.taskName(ctx, owner, rec.TaskID)), nil
}

// JobQuery narrows ListJobs.
type JobQuery struct {
	TaskID string
	Status JobStatus
	Limit  int
}

// ListJobs returns the jobs of the user, newest first.
func (j *Jobs) ListJobs(ctx context.Context, user string, q JobQuery) ([]JobInfo, error) {
	owner := principal(user)
	// The status is read from the chats, so the limit applies after the filter.
	recs, err := j.o.Store.ListJobs(ctx, owner, jobs.Filter{TaskID: q.TaskID})
	if err != nil {
		return nil, err
	}
	return j.infos(ctx, recs, q), nil
}

func (j *Jobs) infos(ctx context.Context, recs []jobs.Job, q JobQuery) []JobInfo {
	names := map[[2]string]string{}
	out := make([]JobInfo, 0, len(recs))
	for _, rec := range recs {
		k := [2]string{rec.Owner, rec.TaskID}
		name, ok := names[k]
		if !ok {
			name = j.taskName(ctx, rec.Owner, rec.TaskID)
			names[k] = name
		}
		info := j.info(ctx, rec, name)
		if q.Status != "" && info.Status != q.Status {
			continue
		}
		out = append(out, info)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out
}

// AdminJobs returns the jobs of every user, newest first. Only the administrator
// gate may reach it.
func (j *Jobs) AdminJobs(ctx context.Context, q JobQuery) ([]JobInfo, error) {
	recs, err := j.o.Store.AllJobs(ctx)
	if err != nil {
		return nil, err
	}
	if q.TaskID != "" {
		recs = slices.DeleteFunc(recs, func(r jobs.Job) bool { return r.TaskID != q.TaskID })
	}
	return j.infos(ctx, recs, q), nil
}

// AdminJob returns the job with this ID whoever owns it. Only the administrator
// gate may reach it.
func (j *Jobs) AdminJob(ctx context.Context, id string) (JobInfo, error) {
	all, err := j.o.Store.AllJobs(ctx)
	if err != nil {
		return JobInfo{}, err
	}
	i := slices.IndexFunc(all, func(r jobs.Job) bool { return r.ID == id })
	if i < 0 {
		return JobInfo{}, jobs.ErrNotFound
	}
	return j.info(ctx, all[i], j.taskName(ctx, all[i].Owner, all[i].TaskID)), nil
}

// Trace is one step of a job's transcript.
type Trace struct {
	Type    string // reasoning, tool_result or status
	Content string
	At      time.Time
}

// Traces returns the reasoning and the tool results of a job, in order.
func (j *Jobs) Traces(ctx context.Context, user, id string) ([]Trace, error) {
	owner := principal(user)
	if _, err := j.o.Store.GetJob(ctx, owner, id); err != nil {
		return nil, err
	}
	var out []Trace
	var after int64
	const page = 500
	for {
		entries, err := j.svc.d.Store.Entries(ctx, id, after, page)
		if err != nil {
			return out, err
		}
		for _, e := range entries {
			switch e.Role {
			case session.RoleReasoning:
				out = append(out, Trace{Type: "reasoning", Content: e.Text, At: e.At})
			case session.RoleTool:
				out = append(out, Trace{Type: "tool_result", Content: e.Text, At: e.At})
			}
		}
		if len(entries) < page {
			return out, nil
		}
		after = entries[len(entries)-1].Seq
	}
}

// CancelJob stops a running job of the user.
func (j *Jobs) CancelJob(ctx context.Context, user, id string) error {
	owner := principal(user)
	rec, err := j.o.Store.GetJob(ctx, owner, id)
	if err != nil {
		return err
	}
	if info := j.info(ctx, rec, ""); info.Status.Final() {
		return ErrJobEnded
	}
	return j.svc.Stop(ctx, owner, id)
}

// DeleteJob removes a job of the user and its chat. A running job is stopped
// first.
func (j *Jobs) DeleteJob(ctx context.Context, user, id string) error {
	owner := principal(user)
	if _, err := j.o.Store.GetJob(ctx, owner, id); err != nil {
		return err
	}
	if err := j.svc.Delete(ctx, owner, id); err != nil && !errors.Is(err, session.ErrNotFound) {
		return err
	}
	return j.o.Store.DeleteJob(ctx, owner, id)
}

// Rerun starts a new job with the task and the parameters of an earlier one.
// The attachments are not kept, so they are not sent again.
func (j *Jobs) Rerun(ctx context.Context, user, id string) (JobInfo, error) {
	rec, err := j.o.Store.GetJob(ctx, principal(user), id)
	if err != nil {
		return JobInfo{}, err
	}
	return j.Execute(ctx, user, rec.TaskID, RunRequest{Params: rec.Parameters, TriggeredBy: "rerun"})
}

// Sweep removes the jobs that ended before the retention and finishes the
// bookkeeping of jobs whose watcher is gone: their end is recorded and their
// webhooks are delivered. The leader runs it.
func (j *Jobs) Sweep(ctx context.Context) error {
	recs, err := j.o.Store.AllJobs(ctx)
	if err != nil {
		return err
	}
	now := j.o.Clock()
	cutoff := now.AddDate(0, 0, -j.o.RetentionDays)
	for _, rec := range recs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		j.mu.Lock()
		_, mine := j.watch[rec.ID]
		j.mu.Unlock()
		if mine {
			continue
		}
		info := j.info(ctx, rec, "")
		if !info.Status.Final() {
			continue
		}
		if rec.Ended.IsZero() {
			j.finish(ctx, rec, "")
			continue
		}
		if !rec.WebhooksDone && now.Sub(rec.Ended) > webhookGrace {
			j.deliver(ctx, rec)
		}
		if rec.Created.Before(cutoff) {
			if err := j.DeleteJob(ctx, rec.Owner, rec.ID); err != nil {
				j.log.Warn("studio: could not remove an old job", "job", rec.ID, "error", err)
			}
		}
	}
	return nil
}
