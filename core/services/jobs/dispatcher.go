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
	bus          messaging.Broadcaster
	db           *gorm.DB
	instanceID   string
	configLoader ModelConfigLoader // optional: to enrich job events with model config

	// Cancel registry (notetaker pattern)
	cancelRegistry messaging.CancelRegistry

	// The broadcast subscriptions this dispatcher owns for the life of the
	// process. The per-request one SubscribeProgress opens is not here: it
	// belongs to the HTTP handler that opened it and is closed with it.
	cancelSub   messaging.Subscription
	resultSub   messaging.Subscription
	progressSub messaging.Subscription

	// terminalRecheck is how often an open SSE stream re-reads the job row.
	// Zero means DefaultTerminalRecheck. Set once, before Start, and read from
	// HTTP handler goroutines afterwards.
	terminalRecheck time.Duration

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// SetTerminalRecheck sets how often an open job-progress SSE stream re-reads
// the job row while it waits.
//
// It exists so a spec can drive the recovery from a dropped terminal broadcast
// without waiting out DefaultTerminalRecheck. Call it before Start.
func (d *Dispatcher) SetTerminalRecheck(interval time.Duration) {
	d.terminalRecheck = interval
}

// NewDispatcher creates a new distributed job Dispatcher.
//
// The carrier is a messaging.Broadcaster because fan-out is all this type does
// with it and all it may have: everything else a job needs from another replica
// travels on the claim row or on the control RPC's response body, and a
// dispatcher that could reach a request/reply or a queue group through this
// field would be able to reintroduce the publish-to-nobody the claim queue
// replaced.
func NewDispatcher(store *JobStore, bus messaging.Broadcaster, db *gorm.DB, instanceID string) *Dispatcher {
	return &Dispatcher{
		store:      store,
		bus:        bus,
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
	// A cancel that does not arrive is not a cancel that was refused. This
	// carrier is at-most-once with no replay, so a broadcast that is dropped or
	// that lands while this replica is reconnecting reaches nobody, and the
	// registry simply never hears about it. Nothing here may report that as the
	// execution having declined to stop: the only thing this subscription can
	// say is that a cancel DID arrive.
	d.cancelSub, err = messaging.SubscribeJSON(d.bus, messaging.SubjectJobCancelWildcard, func(evt CancelEvent) {
		if d.cancelRegistry.Cancel(evt.JobID) {
			xlog.Info("Cancelled a job on this replica after a broadcast cancel", "jobID", evt.JobID)
		}
	})
	if err != nil {
		return fmt.Errorf("subscribing to cancel events: %w", err)
	}

	// Subscribe to job result events from workers (persist to DB)
	if d.store != nil {
		// The fan-out COPY of a terminal result, and never the only one.
		//
		// This carrier drops a broadcast rather than blocking when one
		// subscriber falls behind, and a result has no successor message, so a
		// subscriber that missed one would never hear about that job again.
		// That is survivable here only because it is not the path the answer
		// travels: the replica that claimed the work persists the terminal line
		// through DispatchLoop.settleClaim BEFORE it releases the claim, so the
		// job row already carries the answer when this broadcast is published.
		// A dropped result therefore costs a live SSE stream its promptness,
		// which jobs/sse.go recovers from by reading the row, and never costs
		// the job its answer. Nothing may be moved onto this subscription that
		// is not also written to a table first.
		d.resultSub, err = messaging.SubscribeJSON(d.bus, messaging.SubjectJobResultWildcard, func(evt JobResultEvent) {
			if err := d.store.UpdateJobStatus(evt.JobID, evt.Status, evt.Result, evt.Error); err != nil {
				xlog.Error("Failed to persist a broadcast job result", "job_id", evt.JobID, "error", err)
			}
		})
		if err != nil {
			return fmt.Errorf("subscribing to result events: %w", err)
		}

		// Subscribe to trace events from workers (persist to DB)
		d.progressSub, err = messaging.SubscribeJSON(d.bus, messaging.SubjectJobProgressWildcard, func(evt ProgressEvent) {
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

// unsubscribeAll nil-checks, unsubscribes, and nils out each broadcast
// subscription this dispatcher owns. Safe to call multiple times.
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

// Cancel broadcasts a cancel request to every replica.
//
// Its error says whether the request was PUBLISHED and nothing else. There is
// no reply, and there is deliberately no attempt to synthesise one: a cancel
// that reached no subscriber and a cancel an execution declined are different
// facts, and this carrier cannot tell them apart, so neither may be reported as
// the other.
func (d *Dispatcher) Cancel(jobID string) error {
	return d.bus.Publish(messaging.SubjectJobCancel(jobID), CancelEvent{
		JobID: jobID,
	})
}

// PublishProgress broadcasts a progress event for SSE bridging.
func (d *Dispatcher) PublishProgress(jobID, status, message string) error {
	return d.bus.Publish(messaging.SubjectJobProgress(jobID), ProgressEvent{
		JobID:   jobID,
		Status:  status,
		Message: message,
	})
}

// SubscribeProgress subscribes to progress events for ONE job, for SSE
// bridging.
//
// The subject is the exact one SubjectJobProgress builds and never the
// wildcard. On the wildcard this would be a stream showing every job in the
// deployment to every client watching any of them, which is a data-boundary
// rather than a display bug, and the subscription is opened and closed per HTTP
// request so the caller MUST close it.
func (d *Dispatcher) SubscribeProgress(jobID string, handler func(ProgressEvent)) (messaging.Subscription, error) {
	return messaging.SubscribeJSON(d.bus, messaging.SubjectJobProgress(jobID), handler)
}

// PublishTrace broadcasts a trace event for a running job. The frontend
// subscribes and persists traces to the database.
func (d *Dispatcher) PublishTrace(jobID, traceType, traceContent string) error {
	return d.bus.Publish(messaging.SubjectJobProgress(jobID), ProgressEvent{
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
