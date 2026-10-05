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

// JobEvent is the NATS message payload for job distribution.
type JobEvent struct {
	JobID  string `json:"job_id"`
	TaskID string `json:"task_id"`
	UserID string `json:"user_id"`

	// Enriched payload: set by the frontend so the worker needs no DB access.
	Job         *JobRecord          `json:"job,omitempty"`
	Task        *TaskRecord         `json:"task,omitempty"`
	ModelConfig *config.ModelConfig `json:"model_config,omitempty"` // included so agent workers don't need API access for model config
}

// ProgressEvent is the NATS message payload for progress updates.
type ProgressEvent struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`

	// Trace data (streamed in real-time from worker)
	TraceType    string `json:"trace_type,omitempty"`    // "reasoning", "tool_call", "tool_result", "status"
	TraceContent string `json:"trace_content,omitempty"` // trace payload
}

// JobResultEvent is the NATS message for the final job result (terminal state).
// Published by the worker when execution finishes. The frontend subscribes and
// persists the result to PostgreSQL.
type JobResultEvent struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"` // "completed", "failed", "cancelled"
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// CancelEvent is the NATS message payload for job cancellation.
type CancelEvent struct {
	JobID string `json:"job_id"`
}

// Dispatcher hands jobs to the work queue, persists the results and traces
// workers publish, and coordinates cron execution via PostgreSQL advisory locks.
type Dispatcher struct {
	store        *JobStore
	queue        messaging.WorkQueue
	nats         messaging.Broadcaster
	db           *gorm.DB
	instanceID   string
	configLoader ModelConfigLoader // optional: to enrich job events with model config

	// NATS subscriptions
	resultSub   messaging.Subscription
	progressSub messaging.Subscription

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// NewDispatcher creates a new distributed job Dispatcher. Jobs leave through
// queue and workers consume them; nc carries cancel, progress and result
// fan-out.
func NewDispatcher(store *JobStore, queue messaging.WorkQueue, nc messaging.Broadcaster, db *gorm.DB, instanceID string) *Dispatcher {
	return &Dispatcher{
		store:      store,
		queue:      queue,
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

// Start subscribes to the results and traces workers publish and starts the
// cron leader loop. It consumes no jobs: workers do.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)
	success := false
	defer func() {
		if !success {
			d.unsubscribeAll()
		}
	}()

	var err error
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
	if d.resultSub != nil {
		d.resultSub.Unsubscribe()
		d.resultSub = nil
	}
	if d.progressSub != nil {
		d.progressSub.Unsubscribe()
		d.progressSub = nil
	}
}

// Stop cleans up subscriptions and stops the cron leader loop.
func (d *Dispatcher) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.unsubscribeAll()
}

// Enqueue hands a job to the work queue for distributed processing.
// The event is enriched with the full Job and Task records so that the
// worker does not need direct database access.
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

	kind := messaging.WorkTask
	if evt.ModelConfig != nil && evt.ModelConfig.MCP.HasMCPServers() {
		kind = messaging.WorkMCPCI
	}

	// Enqueue takes no ctx from its callers (an HTTP handler and the cron
	// loop), and the NATS carrier ignores it anyway.
	return d.queue.Enqueue(context.Background(), kind, evt)
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
			d.store.UpdateJobStatus(job.ID, "failed", "", "NATS enqueue failed: "+err.Error())
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
