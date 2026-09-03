package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/dbutil"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// JobEvent is the payload of a task or mcp-ci claim, and the request body of
// the control verb that carries it to a worker.
type JobEvent struct {
	JobID  string `json:"job_id"`
	TaskID string `json:"task_id"`
	UserID string `json:"user_id"`

	// Enriched payload: set by the frontend so the worker needs no DB access.
	Job         *JobRecord          `json:"job,omitempty"`
	Task        *TaskRecord         `json:"task,omitempty"`
	ModelConfig *config.ModelConfig `json:"model_config,omitempty"` // included so agent workers don't need API access for model config
}

// ProgressEvent is the payload of a progress broadcast, published by a worker
// as a re-broadcast REQUEST and by a frontend directly.
type ProgressEvent struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`

	// Trace data (streamed in real-time from worker)
	TraceType    string `json:"trace_type,omitempty"`    // "reasoning", "tool_call", "tool_result", "status"
	TraceContent string `json:"trace_content,omitempty"` // trace payload
}

// JobResultEvent is the broadcast form of a terminal job result.
//
// It is no longer how the result REACHES the frontend: that is the reply line of
// the control verb, which the claiming replica persists before it releases the
// claim. This is the fan-out copy, so an SSE stream open on a replica that
// claimed nothing still sees the job finish.
type JobResultEvent struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"` // "completed", "failed", "cancelled"
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// CancelEvent is the broadcast payload for job cancellation.
type CancelEvent struct {
	JobID string `json:"job_id"`
}

// Dispatcher enqueues jobs as claim rows, bridges the progress and result
// broadcasts a worker asks for onto the database, and coordinates cron
// execution via PostgreSQL advisory locks.
//
// It no longer CONSUMES anything. Dispatch is a claim on the job store (see
// DispatchLoop), so this type has no worker function, no queue subscription and
// no concurrency limiter: there is nothing here to limit.
type Dispatcher struct {
	store        *JobStore
	nats         messaging.MessagingClient
	db           *gorm.DB
	instanceID   string
	configLoader ModelConfigLoader // optional: to enrich job events with model config

	// Cancel registry (notetaker pattern)
	cancelRegistry messaging.CancelRegistry

	// NATS subscriptions
	cancelSub   messaging.Subscription
	resultSub   messaging.Subscription
	progressSub messaging.Subscription

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// NewDispatcher creates a new distributed job Dispatcher.
func NewDispatcher(store *JobStore, nc messaging.MessagingClient, db *gorm.DB, instanceID string) *Dispatcher {
	return &Dispatcher{
		store:      store,
		nats:       nc,
		db:         db,
		instanceID: instanceID,
	}
}

// ModelConfigLoader loads model configurations by name.
type ModelConfigLoader interface {
	GetModelConfig(name string) (config.ModelConfig, bool)
}

// SetModelConfigLoader sets the model config loader for enriching job events.
func (d *Dispatcher) SetModelConfigLoader(cl ModelConfigLoader) {
	d.configLoader = cl
}

// Start subscribes to the job broadcasts and starts the cron leader loop.
//
// It no longer listens for JOBS. Dispatch is a claim on the job store (see
// DispatchLoop); what this subscribes to is the fan-out of a worker's cancels,
// results and traces, which every replica must see because the SSE stream a
// user is watching may be open on a replica that claimed nothing.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)
	success := false
	defer func() {
		if !success {
			d.unsubscribeAll()
		}
	}()

	// No job-queue subscription. Dispatch is a claim on the job store, taken by
	// a frontend replica's DispatchLoop and driven on an agent worker over that
	// worker's own tunnel, so there is no subject for this dispatcher to
	// consume and no queue group left to be one of.
	//
	// What remains here are the two BROADCAST subscriptions below. A worker's
	// progress and result lines still reach every replica, because an SSE
	// stream may be open on a replica that did not claim the work; they arrive
	// as re-broadcasts of the lines the claiming replica read off the response
	// body (see nodes.Rebroadcaster), rather than as publishes from a worker.
	var err error

	// Subscribe to cancel events (broadcast to all — each instance checks its registry)
	d.cancelSub, err = messaging.SubscribeJSON(d.nats, messaging.SubjectJobCancelWildcard, func(evt CancelEvent) {
		if d.cancelRegistry.Cancel(evt.JobID) {
			xlog.Info("Cancelled job via NATS", "jobID", evt.JobID)
		}
	})
	if err != nil {
		return fmt.Errorf("subscribing to cancel events: %w", err)
	}

	// Subscribe to job result events from workers (persist to DB)
	if d.store != nil {
		d.resultSub, err = messaging.SubscribeJSON(d.nats, messaging.SubjectJobResultWildcard, func(evt JobResultEvent) {
			d.store.UpdateJobStatus(evt.JobID, evt.Status, evt.Result, evt.Error)
		})
		if err != nil {
			return fmt.Errorf("subscribing to result events: %w", err)
		}

		// Subscribe to trace events from workers (persist to DB)
		d.progressSub, err = messaging.SubscribeJSON(d.nats, messaging.SubjectJobProgressWildcard, func(evt ProgressEvent) {
			if evt.TraceType != "" && evt.TraceContent != "" {
				if err := d.store.AppendJobTrace(evt.JobID, evt.TraceType, evt.TraceContent); err != nil {
					xlog.Error("Failed to append job trace", "job_id", evt.JobID, "trace_type", evt.TraceType, "error", err)
				}
			}
		})
		if err != nil {
			return fmt.Errorf("subscribing to progress events: %w", err)
		}
	}

	// Start cron leader loop
	go d.cronLeaderLoop()

	success = true
	xlog.Info("Job dispatcher started", "instance", d.instanceID)
	return nil
}

// unsubscribeAll nil-checks, unsubscribes, and nils out each NATS subscription.
// Safe to call multiple times.
func (d *Dispatcher) unsubscribeAll() {
	if d.cancelSub != nil {
		d.cancelSub.Unsubscribe()
		d.cancelSub = nil
	}
	if d.resultSub != nil {
		d.resultSub.Unsubscribe()
		d.resultSub = nil
	}
	if d.progressSub != nil {
		d.progressSub.Unsubscribe()
		d.progressSub = nil
	}
}

// Stop cleans up subscriptions and cancels running jobs.
func (d *Dispatcher) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.unsubscribeAll()
}

// Enqueue writes ONE claim row for a job.
//
// It replaces a publish onto one of two queue subjects, and the difference is
// the point of the change: a publish to a queue group nobody is subscribed to
// succeeds and the job stays `pending` for ever with no trace, while a claim
// row nobody claims is still in the table. The kind the row carries is what the
// subject used to choose.
//
// The event is enriched with the full Job and Task records for the same reason
// it always was: an agent worker has no database access, so everything it needs
// travels with the work.
func (d *Dispatcher) Enqueue(jobID, taskID, userID string) error {
	evt := JobEvent{
		JobID:  jobID,
		TaskID: taskID,
		UserID: userID,
	}

	// Enrich with full records from DB (frontend has DB access)
	if d.store != nil {
		if job, err := d.store.GetJob(jobID); err == nil {
			evt.Job = job
		}
		if task, err := d.store.GetTask(taskID); err == nil {
			evt.Task = task
			// Include model config so agent workers don't need API access
			if d.configLoader != nil && task.Model != "" {
				if cfg, ok := d.configLoader.GetModelConfig(task.Model); ok {
					evt.ModelConfig = &cfg
				}
			}
		}
	}

	kind := ClaimKindTask
	if evt.ModelConfig != nil && evt.ModelConfig.MCP.HasMCPServers() {
		kind = ClaimKindMCPCI
	}

	// Background rather than this dispatcher's own lifecycle context, and
	// deliberately. Enqueue is reachable from an HTTP handler on any goroutine,
	// while d.ctx is written by Start, so reading it here would be a data race;
	// and borrowing it would make a queued job's fate depend on whether the
	// dispatcher happened to be shutting down. It is one INSERT.
	if _, err := EnqueueClaim(context.Background(), d.db, kind, evt); err != nil {
		return err
	}
	return nil
}

// Cancel publishes a cancel event to NATS (broadcast to all instances).
func (d *Dispatcher) Cancel(jobID string) error {
	return d.nats.Publish(messaging.SubjectJobCancel(jobID), CancelEvent{
		JobID: jobID,
	})
}

// PublishProgress publishes a progress event for SSE bridging.
func (d *Dispatcher) PublishProgress(jobID, status, message string) error {
	return d.nats.Publish(messaging.SubjectJobProgress(jobID), ProgressEvent{
		JobID:   jobID,
		Status:  status,
		Message: message,
	})
}

// SubscribeProgress subscribes to progress events for a specific job (for SSE bridging).
func (d *Dispatcher) SubscribeProgress(jobID string, handler func(ProgressEvent)) (messaging.Subscription, error) {
	return messaging.SubscribeJSON(d.nats, messaging.SubjectJobProgress(jobID), handler)
}

// PublishTrace publishes a trace event for a running job via NATS.
// The frontend subscribes and persists traces to DB.
func (d *Dispatcher) PublishTrace(jobID, traceType, traceContent string) error {
	return d.nats.Publish(messaging.SubjectJobProgress(jobID), ProgressEvent{
		JobID:        jobID,
		TraceType:    traceType,
		TraceContent: traceContent,
	})
}

// cronLeaderLoop runs every 15 seconds. Only one instance wins the advisory lock
// and runs due cron tasks. Other instances skip. (notetaker pattern)
func (d *Dispatcher) cronLeaderLoop() {
	advisorylock.RunLeaderLoop(d.ctx, d.db, advisorylock.KeyCronScheduler, 15*time.Second, d.runDueCronTasks)
}

// runDueCronTasks checks all cron tasks and enqueues any that are due.
func (d *Dispatcher) runDueCronTasks() {
	// Reap jobs stuck in "running" state (worker crash recovery)
	if d.store != nil {
		if reaped, err := d.store.ReapStuckJobs(30 * time.Minute); err != nil {
			xlog.Warn("Failed to reap stuck jobs", "error", err)
		} else if reaped > 0 {
			xlog.Info("Reaped stuck jobs", "count", reaped)
		}
	}

	tasks, err := d.store.ListCronTasks()
	if err != nil {
		xlog.Error("Failed to list cron tasks", "error", err)
		return
	}

	for _, task := range tasks {
		if task.Cron == "" || !task.Enabled {
			continue
		}

		if !d.isCronDue(task) {
			continue
		}

		// Create and enqueue a job for this cron task
		var params map[string]string
		dbutil.UnmarshalJSON(task.CronParametersJSON, &params)

		job := &JobRecord{
			TaskID:         task.ID,
			UserID:         task.UserID,
			Status:         "pending",
			ParametersJSON: task.CronParametersJSON,
			TriggeredBy:    "cron",
		}
		if err := d.store.CreateJob(job); err != nil {
			xlog.Error("Failed to create cron job", "taskID", task.ID, "error", err)
			continue
		}

		if err := d.Enqueue(job.ID, task.ID, task.UserID); err != nil {
			xlog.Error("Failed to enqueue cron job, marking as failed", "jobID", job.ID, "error", err)
			if uerr := d.store.UpdateJobStatus(job.ID, "failed", "", "enqueueing the job failed: "+err.Error()); uerr != nil {
				xlog.Error("Failed to mark an unqueueable cron job as failed", "jobID", job.ID, "error", uerr)
			}
		} else {
			xlog.Info("Cron job enqueued", "taskID", task.ID, "jobID", job.ID)
		}
	}
}

// cronParser supports standard 5-field cron expressions and descriptors like @every 5m.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// isCronDue checks if a cron task should run now by parsing the cron expression
// and comparing the next scheduled run against the last execution time.
func (d *Dispatcher) isCronDue(task TaskRecord) bool {
	schedule, err := cronParser.Parse(task.Cron)
	if err != nil {
		xlog.Warn("Invalid cron expression, skipping task", "taskID", task.ID, "cron", task.Cron, "error", err)
		return false
	}

	// Find the most recent job for this task triggered by cron
	var lastJob JobRecord
	err = d.db.Where("task_id = ? AND triggered_by = ?", task.ID, "cron").
		Order("created_at DESC").First(&lastJob).Error
	if err != nil {
		// No previous job — it's due
		return true
	}

	// Guard: don't create if a recent cron job is still pending/running
	if lastJob.Status == "pending" || lastJob.Status == "running" {
		return false
	}

	// Compute the cron interval from two consecutive ticks after the last job.
	// Use elapsed time since the last job rather than clock-aligned ticks to
	// avoid re-triggering when the job was created shortly before a tick boundary.
	nextRun := schedule.Next(lastJob.CreatedAt)
	minInterval := schedule.Next(nextRun).Sub(nextRun)
	return time.Since(lastJob.CreatedAt) >= minInterval
}
