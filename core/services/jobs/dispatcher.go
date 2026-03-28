package jobs

import (
	"context"
	"errors"
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
	Job         *JobRecord         `json:"job,omitempty"`
	Task        *TaskRecord        `json:"task,omitempty"`
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

// WorkerFunc is the function signature for processing a job.
// It receives the job record, task record, and a context that will be cancelled
// if the job is cancelled via NATS.
type WorkerFunc func(ctx context.Context, job *JobRecord, task *TaskRecord) error

// Dispatcher distributes jobs across instances via NATS queue groups
// and coordinates cron execution via PostgreSQL advisory locks.
type Dispatcher struct {
	store        *JobStore
	nats         messaging.MessagingClient
	db           *gorm.DB
	instanceID   string
	configLoader ModelConfigLoader // optional: to enrich job events with model config

	// Worker function (set by the application)
	workerFn WorkerFunc

	// Cancel registry (notetaker pattern)
	cancelRegistry messaging.CancelRegistry

	// NATS subscriptions
	jobSub      messaging.Subscription
	cancelSub   messaging.Subscription
	resultSub   messaging.Subscription
	progressSub messaging.Subscription

	// Concurrency limiter; nil = unlimited
	sem chan struct{}

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// NewDispatcher creates a new distributed job Dispatcher.
// maxConcurrent limits the number of concurrent job goroutines; 0 means unlimited.
func NewDispatcher(store *JobStore, nc messaging.MessagingClient, db *gorm.DB, instanceID string, maxConcurrent int) *Dispatcher {
	d := &Dispatcher{
		store:      store,
		nats:       nc,
		db:         db,
		instanceID: instanceID,
	}
	if maxConcurrent > 0 {
		d.sem = make(chan struct{}, maxConcurrent)
	}
	return d
}

// ModelConfigLoader loads model configurations by name.
type ModelConfigLoader interface {
	GetModelConfig(name string) (config.ModelConfig, bool)
}

// SetWorkerFunc sets the function that processes jobs.
func (d *Dispatcher) SetWorkerFunc(fn WorkerFunc) {
	d.workerFn = fn
}

// SetModelConfigLoader sets the model config loader for enriching job events.
func (d *Dispatcher) SetModelConfigLoader(cl ModelConfigLoader) {
	d.configLoader = cl
}

// Start begins listening for jobs via NATS and starts the cron leader loop.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)
	success := false
	defer func() {
		if !success {
			if d.jobSub != nil {
				d.jobSub.Unsubscribe()
				d.jobSub = nil
			}
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
	}()

	// Subscribe to job queue only if a worker function is configured.
	// In distributed mode, the frontend dispatcher publishes jobs but does not consume them —
	// agent workers pick them up from the same NATS queue.
	var err error
	if d.workerFn != nil {
		d.jobSub, err = messaging.QueueSubscribeJSON(d.nats, messaging.SubjectJobsNew, messaging.QueueWorkers, func(evt JobEvent) {
			if d.sem != nil {
				d.sem <- struct{}{}
			}
			go func() {
				if d.sem != nil {
					defer func() { <-d.sem }()
				}
				d.processJob(evt)
			}()
		})
		if err != nil {
			return fmt.Errorf("subscribing to job queue: %w", err)
		}
	}

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

// Stop cleans up subscriptions and cancels running jobs.
func (d *Dispatcher) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	if d.jobSub != nil {
		d.jobSub.Unsubscribe()
	}
	if d.cancelSub != nil {
		d.cancelSub.Unsubscribe()
	}
	if d.resultSub != nil {
		d.resultSub.Unsubscribe()
	}
	if d.progressSub != nil {
		d.progressSub.Unsubscribe()
	}
}

// Enqueue publishes a job to the NATS queue for distributed processing.
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

	subject := messaging.SubjectJobsNew
	if evt.ModelConfig != nil && evt.ModelConfig.MCP.HasMCPServers() {
		subject = messaging.SubjectMCPCIJobsNew
	}

	return d.nats.Publish(subject, evt)
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

// processJob is called by the NATS queue subscriber to execute a job.
// It prefers Job+Task from the enriched NATS payload (no DB needed).
// Results are published back via NATS for the frontend to persist.
func (d *Dispatcher) processJob(evt JobEvent) {
	if d.workerFn == nil {
		xlog.Error("No worker function set for job dispatcher")
		d.publishResult(evt.JobID, "failed", "", "no worker function configured")
		return
	}

	// Prefer enriched payload; fall back to DB for backward compat
	job := evt.Job
	if job == nil && d.store != nil {
		var err error
		job, err = d.store.GetJob(evt.JobID)
		if err != nil {
			xlog.Error("Failed to load job", "jobID", evt.JobID, "error", err)
			return
		}
	}
	if job == nil {
		xlog.Error("No job data available", "jobID", evt.JobID)
		return
	}

	task := evt.Task
	if task == nil && d.store != nil {
		var err error
		task, err = d.store.GetTask(job.TaskID)
		if err != nil {
			xlog.Error("Failed to load task for job", "jobID", evt.JobID, "taskID", job.TaskID, "error", err)
			d.publishResult(evt.JobID, "failed", "", "task not found")
			return
		}
	}
	if task == nil {
		xlog.Error("No task data available", "jobID", evt.JobID)
		d.publishResult(evt.JobID, "failed", "", "task not found")
		return
	}

	// Pre-register so cancels arriving before context creation are captured
	cancelled := make(chan struct{}, 1)
	d.cancelRegistry.Register(evt.JobID, func() {
		select {
		case cancelled <- struct{}{}:
		default:
		}
	})

	ctx, cancelFn := context.WithCancel(d.ctx)
	d.cancelRegistry.Register(evt.JobID, cancelFn) // overwrite with real cancel

	// Check if cancel arrived during the registration window
	select {
	case <-cancelled:
		cancelFn()
	default:
	}

	defer func() {
		d.cancelRegistry.Deregister(evt.JobID)
		cancelFn()
	}()

	// Check if already cancelled before starting
	select {
	case <-ctx.Done():
		d.publishResult(evt.JobID, "cancelled", "", "")
		return
	default:
	}

	// Mark as running
	job.FrontendID = d.instanceID
	d.PublishProgress(evt.JobID, "running", "Job started")

	// Execute
	err := d.workerFn(ctx, job, task)

	if errors.Is(ctx.Err(), context.Canceled) {
		d.publishResult(evt.JobID, "cancelled", "", "")
		d.PublishProgress(evt.JobID, "cancelled", "Job cancelled")
		return
	}

	if err != nil {
		d.publishResult(evt.JobID, "failed", "", err.Error())
		d.PublishProgress(evt.JobID, "failed", err.Error())
		return
	}

	// Publish completion — result is set on the job by workerFn
	d.publishResult(evt.JobID, "completed", job.Result, "")
	d.PublishProgress(evt.JobID, "completed", "Job completed")
}

// publishResult publishes the terminal job result via NATS.
// The frontend subscribes to these events and persists to DB.
func (d *Dispatcher) publishResult(jobID, status, result, errMsg string) {
	PublishJobResult(d.nats, jobID, status, result, errMsg)
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
	advisorylock.RunLeaderLoop(d.ctx, d.db, messaging.AdvisoryLockCronScheduler, 15*time.Second, d.runDueCronTasks)
}

// runDueCronTasks checks all cron tasks and enqueues any that are due.
func (d *Dispatcher) runDueCronTasks() {
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
			xlog.Error("Failed to enqueue cron job", "jobID", job.ID, "error", err)
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

	nextRun := schedule.Next(lastJob.CreatedAt)
	return time.Now().After(nextRun)
}
