package schema

import (
	"time"
)

// Task represents a reusable agent task definition: a prompt template run by a
// model, or by a saved agent (agent_id), as a background chat.
type Task struct {
	ID          string `json:"id"`          // UUID
	Name        string `json:"name"`        // User-friendly name
	Description string `json:"description"` // Optional description
	// Model is the model the job talks to. It may be empty when agent_id names a
	// saved agent that has a model.
	Model string `json:"model"`
	// AgentID is optional: a saved agent of the owner. The job gets only the
	// agent's grants and instructions, as a chat with that agent would.
	AgentID   string    `json:"agent_id,omitempty"`
	Prompt    string    `json:"prompt"` // Template prompt (supports Go template .param syntax)
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Enabled   *bool     `json:"enabled,omitempty"` // Can be disabled without deletion; absent means enabled

	// Cron and CronParameters are refused: scheduling is done by the triggers of
	// the chats, which can run a saved agent on a schedule.
	Cron           string            `json:"cron,omitempty"`
	CronParameters map[string]string `json:"cron_parameters,omitempty"`

	// Webhook configuration (for notifications).
	// Supports multiple webhook endpoints.
	// Webhooks can handle both success and failure cases using template variables:
	// .Job (Job object), .Task (Task object), .Result (if successful),
	// .Error (if failed), .Status (job status string).
	Webhooks []WebhookConfig `json:"webhooks,omitempty"` // Webhook configs for job completion notifications

	// MultimediaSources is refused: it fed the cron jobs, which are triggers now.
	MultimediaSources []MultimediaSourceConfig `json:"multimedia_sources,omitempty"`
}

// WebhookConfig represents configuration for sending webhook notifications
type WebhookConfig struct {
	URL             string            `json:"url"`                        // Webhook endpoint URL
	Method          string            `json:"method"`                     // HTTP method (POST, PUT, PATCH) - default: POST
	Headers         map[string]string `json:"headers,omitempty"`          // Custom headers (e.g., Authorization)
	PayloadTemplate string            `json:"payload_template,omitempty"` // Optional template for payload
	// If PayloadTemplate is empty, uses default JSON structure.
	// Available template variables: .Job, .Task, .Result, .Error, .Status.
}

// MultimediaSourceConfig represents configuration for fetching multimedia content
// Used in cron jobs to periodically fetch multimedia from URLs with custom headers
type MultimediaSourceConfig struct {
	Type    string            `json:"type"`              // "image", "video", "audio", "file"
	URL     string            `json:"url"`               // URL to fetch from
	Headers map[string]string `json:"headers,omitempty"` // Custom headers for HTTP request (e.g., Authorization)
}

type MultimediaAttachment struct {
	Images []string `json:"images,omitempty"`
	Videos []string `json:"videos,omitempty"`
	Audios []string `json:"audios,omitempty"`
	Files  []string `json:"files,omitempty"`
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

	// Images, Videos, Audios and Files are the number of attachments the job
	// was started with; the content is not kept.
	Attachments int `json:"attachments,omitempty"`
}

// JobTrace represents a single execution trace entry
type JobTrace struct {
	Type      string         `json:"type"`                // "reasoning", "tool_call", "tool_result", "status"
	Content   string         `json:"content"`             // The actual trace content
	Timestamp time.Time      `json:"timestamp"`           // When this trace occurred
	ToolName  string         `json:"tool_name,omitempty"` // Tool name (for tool_call/tool_result)
	Arguments map[string]any `json:"arguments,omitempty"` // Tool arguments or result data
}

// JobExecutionRequest represents a request to execute a job
type JobExecutionRequest struct {
	TaskID     string            `json:"task_id"`    // Required
	Parameters map[string]string `json:"parameters"` // Optional, for templating
	// Attachments for the job, as data URIs (data:<mime>;base64,<data>). An
	// address is not fetched: the server never makes a request on behalf of a
	// job's caller. They are saved in the owner's files for the job to read.
	Images []string `json:"images,omitempty"`
	Videos []string `json:"videos,omitempty"`
	Audios []string `json:"audios,omitempty"`
	Files  []string `json:"files,omitempty"`
}

// JobExecutionResponse represents the response after creating a job
type JobExecutionResponse struct {
	JobID  string `json:"job_id"` // unique job identifier
	Status string `json:"status"` // initial status (pending)
	URL    string `json:"url"`    // URL to poll for job status
}
