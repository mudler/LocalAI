package schema

import (
	"time"
)

// Task represents a reusable agent task definition
type Task struct {
	ID          string            `json:"id"`           // UUID
	Name        string            `json:"name"`         // User-friendly name
	Description string            `json:"description"`  // Optional description
	Model       string            `json:"model"`        // Model name (must have MCP config)
	Prompt      string            `json:"prompt"`       // Template prompt (supports {{.param}} syntax)
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Enabled     bool              `json:"enabled"`     // Can be disabled without deletion
	Cron        string            `json:"cron,omitempty"` // Optional cron expression

	// Webhook configuration (for notifications)
	WebhookURL      string `json:"webhook_url,omitempty"`       // Optional webhook URL
	WebhookAuth     string `json:"webhook_auth,omitempty"`       // Optional auth header
	WebhookTemplate string `json:"webhook_template,omitempty"`   // Optional custom payload template

	// Result push configuration (for pushing results to external APIs)
	// Support multiple result push endpoints
	ResultPush        []ResultPushConfig `json:"result_push,omitempty"`        // Result push configs for successful jobs
	ResultPushFailure []ResultPushConfig `json:"result_push_failure,omitempty"` // Result push configs for failed jobs
}

// ResultPushConfig represents configuration for pushing job results to external APIs
type ResultPushConfig struct {
	URL            string            `json:"url"`                      // REST API endpoint URL
	Method         string            `json:"method"`                   // HTTP method (POST, PUT, PATCH) - default: POST
	Headers        map[string]string `json:"headers,omitempty"`       // Custom headers (e.g., Authorization) - static headers only
	PayloadTemplate string            `json:"payload_template,omitempty"` // Optional template for payload
	// If PayloadTemplate is empty, uses default JSON structure
}

// JobStatus represents the status of a job
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// Job represents a single execution instance of a task
type Job struct {
	ID          string            `json:"id"`           // UUID
	TaskID      string            `json:"task_id"`      // Reference to Task
	Status      JobStatus         `json:"status"`       // pending, running, completed, failed, cancelled
	Parameters  map[string]string `json:"parameters"`   // Template parameters
	Result      string            `json:"result,omitempty"` // Agent response
	Error       string            `json:"error,omitempty"`  // Error message if failed
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	TriggeredBy string            `json:"triggered_by"` // "manual", "cron", "api"

	// Webhook delivery tracking
	WebhookSent   bool       `json:"webhook_sent,omitempty"`
	WebhookSentAt *time.Time `json:"webhook_sent_at,omitempty"`
	WebhookError  string     `json:"webhook_error,omitempty"` // Error if webhook failed

	// Result push tracking
	ResultPushed   bool       `json:"result_pushed,omitempty"`
	ResultPushedAt *time.Time `json:"result_pushed_at,omitempty"`
	ResultPushError string    `json:"result_push_error,omitempty"` // Error if push failed
}

// JobExecutionRequest represents a request to execute a job
type JobExecutionRequest struct {
	TaskID     string            `json:"task_id"`     // Required
	Parameters map[string]string `json:"parameters"` // Optional, for templating
}

// JobExecutionResponse represents the response after creating a job
type JobExecutionResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
	URL    string `json:"url"` // URL to check job status
}

// TasksFile represents the structure of agent_tasks.json
type TasksFile struct {
	Tasks []Task `json:"tasks"`
}

// JobsFile represents the structure of agent_jobs.json
type JobsFile struct {
	Jobs        []Job     `json:"jobs"`
	LastCleanup time.Time `json:"last_cleanup,omitempty"`
}

