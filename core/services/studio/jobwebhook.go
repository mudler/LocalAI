package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// WebhookCall is one HTTP request a job makes when it ends.
type WebhookCall struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// WebhookSender makes a webhook request and returns an error unless the answer
// is a 2xx.
type WebhookSender interface {
	Send(ctx context.Context, call WebhookCall) error
}

// webhookClient sends webhooks with the hardened client: no redirect is
// followed, and an address on a private or internal network is refused at dial
// time unless the administrator allows it.
type webhookClient struct {
	allowPrivate func() bool
}

func newWebhookClient(allowPrivate func() bool) WebhookSender {
	return &webhookClient{allowPrivate: allowPrivate}
}

func (c *webhookClient) private() bool { return c.allowPrivate != nil && c.allowPrivate() }

func (c *webhookClient) Send(ctx context.Context, call WebhookCall) error {
	if err := checkWebhookURL(call.URL, c.allowPrivate); err != nil {
		return err
	}
	var hc *http.Client
	if c.private() {
		hc = httpclient.NewWithTimeout(30 * time.Second)
	} else {
		hc = httpclient.NewWithTimeout(30*time.Second, httpclient.WithExternalOnly())
	}
	req, err := http.NewRequestWithContext(ctx, call.Method, call.URL, bytes.NewReader(call.Body))
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range call.Headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	return nil
}

// checkWebhookURL checks the address of a webhook: http or https, a host, no
// credentials, and no plainly internal host unless the administrator allows it.
func checkWebhookURL(raw string, allowPrivate func() bool) error {
	return mcpconns.CheckURL(raw, allowPrivate == nil || !allowPrivate())
}

// webhookJob and webhookTask are the data a payload template sees. The field
// names are the ones the templates of the old agent jobs used.
type webhookJob struct {
	ID          string
	TaskID      string
	Status      string
	Parameters  map[string]string
	Result      string
	Error       string
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	TriggeredBy string
}

type webhookTask struct {
	ID          string
	Name        string
	Description string
	Model       string
	Prompt      string
}

func utcPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// payload builds the body of a webhook: the task's template, or the default
// JSON document.
func (w webhookData) payload(hook jobs.Webhook) ([]byte, error) {
	if hook.PayloadTemplate == "" {
		return json.Marshal(map[string]any{
			"job_id": w.job.ID, "task_id": w.job.TaskID, "task_name": w.task.Name, "status": w.job.Status,
			"result": w.job.Result, "error": w.job.Error, "parameters": w.job.Parameters,
			"started_at": w.job.StartedAt, "completed_at": w.job.CompletedAt,
		})
	}
	tmpl, err := template.New("payload").Funcs(jobs.PayloadFuncs()).Parse(hook.PayloadTemplate)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"Job": w.job, "Task": w.task, "Result": w.job.Result, "Error": w.job.Error,
		"Parameters": w.job.Parameters, "Status": w.job.Status,
	})
	return buf.Bytes(), err
}

type webhookData struct {
	job  webhookJob
	task webhookTask
}

// deliver calls the webhooks of the job's task, once per job: a record that is
// already done is left alone. It records whether any call succeeded.
func (j *Jobs) deliver(ctx context.Context, rec jobs.Job) {
	if rec.WebhooksDone {
		return
	}
	task, err := j.o.Store.GetTask(ctx, rec.Owner, rec.TaskID)
	if errors.Is(err, jobs.ErrNotFound) || (err == nil && len(task.Webhooks) == 0) {
		rec.WebhooksDone = true
		if err := j.o.Store.PutJob(ctx, rec); err != nil {
			j.log.Warn("studio: could not record that a job has no webhooks", "job", rec.ID, "error", err)
		}
		return
	}
	if err != nil {
		j.log.Warn("studio: could not read the task of a job for its webhooks", "job", rec.ID, "error", err)
		return
	}
	info := j.info(ctx, rec, task.Name)
	data := webhookData{
		job: webhookJob{ID: rec.ID, TaskID: rec.TaskID, Status: string(info.Status), Parameters: rec.Parameters, Result: info.Result,
			Error: info.Error, StartedAt: utcPtr(info.StartedAt), CompletedAt: utcPtr(info.CompletedAt), CreatedAt: rec.Created, TriggeredBy: rec.TriggeredBy},
		task: webhookTask{ID: task.ID, Name: task.Name, Description: task.Description, Model: task.Model, Prompt: task.Prompt},
	}
	var failures []string
	sent := 0
	for _, hook := range task.Webhooks {
		if err := j.sendWebhook(ctx, hook, data); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", hook.URL, err))
			continue
		}
		sent++
	}
	rec.WebhooksDone = true
	switch {
	case len(failures) == 0:
		rec.WebhookSent, rec.WebhookSentAt, rec.WebhookError = true, j.o.Clock().UTC(), ""
	case sent > 0:
		rec.WebhookSent, rec.WebhookSentAt = true, j.o.Clock().UTC()
		rec.WebhookError = fmt.Sprintf("some webhooks failed (%d of %d succeeded): %s", sent, len(task.Webhooks), strings.Join(failures, "; "))
	default:
		rec.WebhookError = "all webhooks failed: " + strings.Join(failures, "; ")
	}
	rec.WebhookError = limitError(rec.WebhookError)
	if err := j.o.Store.PutJob(ctx, rec); err != nil {
		j.log.Warn("studio: could not record the delivery of webhooks", "job", rec.ID, "error", err)
	}
}

// sendWebhook builds and sends one webhook, retrying with the configured backoff.
func (j *Jobs) sendWebhook(ctx context.Context, hook jobs.Webhook, data webhookData) error {
	body, err := data.payload(hook)
	if err != nil {
		return fmt.Errorf("build the payload: %w", err)
	}
	method := hook.Method
	if method == "" {
		method = http.MethodPost
	}
	call := WebhookCall{Method: method, URL: hook.URL, Headers: hook.Headers, Body: body}
	attempts := len(j.o.Backoff) + 1
	for i := 0; ; i++ {
		err = j.o.Webhooks.Send(ctx, call)
		if err == nil {
			return nil
		}
		if i == attempts-1 {
			return fmt.Errorf("failed after %d attempts: %w", attempts, err)
		}
		select {
		case <-time.After(j.o.Backoff[i]):
		case <-ctx.Done():
			return ctx.Err()
		case <-j.done:
			return errors.New("the server is shutting down")
		}
	}
}
