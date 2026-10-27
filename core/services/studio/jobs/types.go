// Package jobs keeps the agent tasks and the metadata of the agent jobs. A task
// is a prompt template with the model or saved agent that runs it and the
// webhooks to call when a job ends. A job is a background chat started from a
// task; the chat holds its transcript, status and result, so this package keeps
// only what the chat cannot: the task, the parameters, who triggered it and
// whether its webhooks were delivered.
//
// Two stores exist: NewJSON (a file under a data directory, or memory when the
// directory is empty) and NewGORM (SQLite or PostgreSQL). jobstest.Run checks
// both.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/Masterminds/sprig/v3"
)

// Errors. A task or job that does not exist, or belongs to another owner, is
// ErrNotFound for every method that takes an owner.
var (
	ErrNotFound  = errors.New("jobs: not found")
	ErrNameTaken = errors.New("jobs: the user already has a task with this name")
	// ErrInvalid wraps the reason a definition is refused.
	ErrInvalid = errors.New("jobs: invalid task")
)

// Limits of a task.
const (
	MaxNameRunes    = 80
	MaxDescription  = 400
	MaxPromptBytes  = 32 << 10
	MaxWebhooks     = 5
	MaxHeaders      = 20
	MaxPayloadBytes = 8 << 10
	MaxTasksPerUser = 200
)

// Webhook is an HTTP call made when a job ends.
type Webhook struct {
	URL    string
	Method string // POST, PUT or PATCH; empty means POST
	// Headers are sent as they are. They may hold a credential, so the API never
	// returns their values.
	Headers         map[string]string
	PayloadTemplate string // empty: the default JSON document
}

// Task is a saved prompt template.
type Task struct {
	ID, Owner   string
	Name        string
	Description string
	Model       string // may be empty when AgentID names an agent with a model
	AgentID     string
	Prompt      string // a text/template with the parameters as the data
	Enabled     bool
	Webhooks    []Webhook
	Created     time.Time
	Updated     time.Time
}

// Job is the record of one run. Status, result, error and traces are read from
// the chat with the same ID.
type Job struct {
	ID, Owner   string // ID is the ID of the chat that runs the job
	TaskID      string
	Parameters  map[string]string
	TriggeredBy string // manual, api, rerun
	Attachments int
	Created     time.Time
	// Ended is when the watcher saw the job end and Error the text of the
	// error event, if the job failed. Both are set once; the chat holds the
	// status and the result.
	Ended time.Time
	Error string
	// WebhooksDone is true once every webhook of the task was tried for the end
	// of this job. WebhookSent tells that at least one succeeded.
	WebhooksDone  bool
	WebhookSent   bool
	WebhookSentAt time.Time
	WebhookError  string
}

// Filter narrows ListJobs.
type Filter struct {
	TaskID string
	// Limit is the most jobs returned, newest first. Zero or less: all.
	Limit int
}

// Store keeps tasks and jobs.
type Store interface {
	// PutTask inserts the task or replaces the one with the same ID and owner.
	// An ID of another owner is ErrNotFound; a name the owner uses for another
	// task is ErrNameTaken. Created is kept when the task exists.
	PutTask(ctx context.Context, t Task) error
	GetTask(ctx context.Context, owner, id string) (Task, error)
	// ListTasks returns the tasks of the owner sorted by name, case-insensitively.
	ListTasks(ctx context.Context, owner string) ([]Task, error)
	// AllTasks returns the tasks of every owner, by owner and name.
	AllTasks(ctx context.Context) ([]Task, error)
	// DeleteTask removes the task. Its jobs stay.
	DeleteTask(ctx context.Context, owner, id string) error

	// PutJob inserts the job or replaces the one with the same ID and owner.
	PutJob(ctx context.Context, j Job) error
	GetJob(ctx context.Context, owner, id string) (Job, error)
	// ListJobs returns the jobs of the owner, newest first.
	ListJobs(ctx context.Context, owner string, f Filter) ([]Job, error)
	// AllJobs returns the jobs of every owner, newest first.
	AllJobs(ctx context.Context) ([]Job, error)
	DeleteJob(ctx context.Context, owner, id string) error
}

// NameKey is the form of a name that must be unique per owner.
func NameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Validate checks the limits of a task. The error wraps ErrInvalid and names the
// field, in words for a person.
func Validate(t Task) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	name := strings.TrimSpace(t.Name)
	switch {
	case name == "":
		return bad("task name is required")
	case utf8.RuneCountInString(name) > MaxNameRunes:
		return bad("task name can have at most %d characters", MaxNameRunes)
	case utf8.RuneCountInString(t.Description) > MaxDescription:
		return bad("the description can have at most %d characters", MaxDescription)
	case strings.TrimSpace(t.Prompt) == "":
		return bad("task prompt is required")
	case len(t.Prompt) > MaxPromptBytes:
		return bad("the prompt can have at most %d bytes", MaxPromptBytes)
	case strings.TrimSpace(t.Model) == "" && strings.TrimSpace(t.AgentID) == "":
		return bad("task model is required (or an agent_id of an agent that has a model)")
	case len(t.Webhooks) > MaxWebhooks:
		return bad("a task can have at most %d webhooks", MaxWebhooks)
	}
	if _, err := template.New("prompt").Parse(t.Prompt); err != nil {
		return bad("the prompt is not a valid template: %v", err)
	}
	for i, w := range t.Webhooks {
		switch {
		case strings.TrimSpace(w.URL) == "":
			return bad("webhook %d has no URL", i+1)
		case w.Method != "" && w.Method != "POST" && w.Method != "PUT" && w.Method != "PATCH":
			return bad("webhook %d method must be POST, PUT or PATCH", i+1)
		case len(w.Headers) > MaxHeaders:
			return bad("webhook %d can have at most %d headers", i+1, MaxHeaders)
		case len(w.PayloadTemplate) > MaxPayloadBytes:
			return bad("webhook %d payload template can have at most %d bytes", i+1, MaxPayloadBytes)
		}
		if w.PayloadTemplate != "" {
			if _, err := template.New("payload").Funcs(PayloadFuncs()).Parse(w.PayloadTemplate); err != nil {
				return bad("webhook %d payload template is not valid: %v", i+1, err)
			}
		}
	}
	return nil
}

// PayloadFuncs returns the functions a payload template may call: the sprig
// functions and json. The ones that read the server's environment or resolve
// names are left out, as a template is written by a user, not the operator.
func PayloadFuncs() template.FuncMap {
	f := sprig.FuncMap()
	for _, name := range []string{"env", "expandenv", "getHostByName"} {
		delete(f, name)
	}
	f["json"] = func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
	return f
}

func (t Task) clone() Task {
	ws := make([]Webhook, len(t.Webhooks))
	for i, w := range t.Webhooks {
		h := make(map[string]string, len(w.Headers))
		for k, v := range w.Headers {
			h[k] = v
		}
		w.Headers = h
		ws[i] = w
	}
	t.Webhooks = ws
	return t
}

func (j Job) clone() Job {
	p := make(map[string]string, len(j.Parameters))
	for k, v := range j.Parameters {
		p[k] = v
	}
	j.Parameters = p
	return j
}
