package schema

import (
	"time"
)

// Task represents a reusable agent task definition
type Task struct {
	ID             string            `json:"id"`          // UUID
	Name           string            `json:"name"`        // User-friendly name
	Description    string            `json:"description"` // Optional description
	Model          string            `json:"model"`       // Model name (must have MCP config)
	Prompt         string            `json:"prompt"`      // Template prompt (supports {{.param}} syntax)
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Enabled        bool              `json:"enabled"`                   // Can be disabled without deletion
	Cron           string            `json:"cron,omitempty"`            // Optional cron expression
	CronParameters map[string]string `json:"cron_parameters,omitempty"` // Parameters to use when executing cron jobs

	// Webhook configuration (for notifications)
	// Support multiple webhook endpoints
	// Webhooks can handle both success and failure cases using template variables:
	// - {{.Job}} - Job object with all fields
	// - {{.Task}} - Task object
	// - {{.Result}} - Job result (if successful)
	// - {{.Error}} - Error message (if failed, empty string if successful)
	// - {{.Status}} - Job status string
	Webhooks []WebhookConfig `json:"webhooks,omitempty"` // Webhook configs for job completion notifications

}

// WebhookConfig represents configuration for sending webhook notifications
type WebhookConfig struct {
	URL             string            `json:"url"`                        // Webhook endpoint URL
	Method          string            `json:"method"`                     // HTTP method (POST, PUT, PATCH) - default: POST
	Headers         map[string]string `json:"headers,omitempty"`          // Custom headers (e.g., Authorization)
	PayloadTemplate string            `json:"payload_template,omitempty"` // Optional template for payload
	// If PayloadTemplate is empty, uses default JSON structure
	// Available template variables:
	// - {{.Job}} - Job object with all fields
	// - {{.Task}} - Task object
	// - {{.Result}} - Job result (if successful)
	// - {{.Error}} - Error message (if failed, empty string if successful)
	// - {{.Status}} - Job status string
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
	ID          string            `json:"id"`               // UUID
	TaskID      string            `json:"task_id"`          // Reference to Task
	Status      JobStatus         `json:"status"`           // pending, running, completed, failed, cancelled
	Parameters  map[string]string `json:"parameters"`       // Template parameters
	Result      string            `json:"result,omitempty"` // Agent response
	Error       string            `json:"error,omitempty"`  // Error message if failed
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	TriggeredBy string            `json:"triggered_by"` // "manual", "cron", "api"

	// Webhook delivery tracking
	WebhookSent   bool       `json:"webhook_sent,omitempty"`
	WebhookSentAt *time.Time `json:"webhook_sent_at,omitempty"`
	WebhookError  string     `json:"webhook_error,omitempty"` // Error if webhook failed

	// Execution traces (reasoning, tool calls, tool results)
	Traces []JobTrace `json:"traces,omitempty"`
}

// JobTrace represents a single execution trace entry
type JobTrace struct {
	Type      string                 `json:"type"`                // "reasoning", "tool_call", "tool_result", "status"
	Content   string                 `json:"content"`             // The actual trace content
	Timestamp time.Time              `json:"timestamp"`           // When this trace occurred
	ToolName  string                 `json:"tool_name,omitempty"` // Tool name (for tool_call/tool_result)
	Arguments map[string]interface{} `json:"arguments,omitempty"` // Tool arguments or result data
}

// JobExecutionRequest represents a request to execute a job
type JobExecutionRequest struct {
	TaskID     string            `json:"task_id"`    // Required
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
