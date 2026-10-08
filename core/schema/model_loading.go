package schema

import "time"

// ModelLoadingStatus describes a cold load that is still in progress. In
// distributed mode a model can take tens of minutes to stage onto a worker,
// which is far longer than a request may be held; a caller that runs out of
// wait budget gets this instead of an anonymous hang or a misleading error.
type ModelLoadingStatus struct {
	// JobID names the load attempt. A cancel must quote it: it is the
	// precondition that keeps a cancel from hitting a replacement attempt.
	JobID string `json:"job_id,omitempty"`
	// LeaseExpiresIn is the seconds left on the owner's lease. It is negative
	// when the lease already ran out, which means the owner is gone.
	LeaseExpiresIn *int `json:"lease_expires_in,omitempty"`
	// CancelRequested is true when an administrator cancelled the attempt.
	CancelRequested bool `json:"cancel_requested,omitempty"`
	// LastError is the cause of a failed attempt.
	LastError string `json:"last_error,omitempty"`
	// Stopping is true while the remote work of a failed attempt is not yet
	// confirmed ended. StopDeadline is when the model is released regardless.
	Stopping     bool       `json:"stopping,omitempty"`
	StopDeadline *time.Time `json:"stop_deadline,omitempty"`
	// RetryAfter is the seconds until a new load may start, for a failed attempt.
	RetryAfter int     `json:"retry_after,omitempty"`
	Model      string  `json:"model"`
	State      string  `json:"state"`
	Node       string  `json:"node,omitempty"`
	Progress   float64 `json:"progress"`
	BytesSent  int64   `json:"bytes_sent"`
	TotalBytes int64   `json:"total_bytes"`
	FileIndex  int     `json:"file_index"`
	TotalFiles int     `json:"total_files"`
	// ETASeconds is omitted rather than guessed until enough bytes have moved
	// for the observed rate to mean anything. A confidently wrong ETA on a
	// twenty-minute wait is worse than none.
	ETASeconds int `json:"eta_seconds,omitempty"`
}

// ModelLoadingResponse is the 503 body served while a model is still loading.
// The `error` envelope keeps OpenAI-client compatibility; `loading` is additive,
// so existing clients ignore it and load-aware ones can render real progress.
type ModelLoadingResponse struct {
	Error   *APIError           `json:"error,omitempty"`
	Loading *ModelLoadingStatus `json:"loading,omitempty"`
}

// ModelLoadCancelRequest is the body of POST /api/models/{id}/load-cancel.
// JobID is the exact attempt to cancel, as load-status reports it.
type ModelLoadCancelRequest struct {
	JobID string `json:"job_id"`
}

// ModelLoadCancelResponse reports what a cancel did. State is "stopped" (the
// worker confirmed the work ended), "stopping" (the cancel is recorded and the
// stop is pending; RetryAfter says when the model is released regardless) or
// "gone" (no such load exists any more). CurrentJobID is set on a conflict.
type ModelLoadCancelResponse struct {
	Model        string `json:"model"`
	JobID        string `json:"job_id"`
	State        string `json:"state"`
	RetryAfter   int    `json:"retry_after,omitempty"`
	CurrentJobID string `json:"current_job_id,omitempty"`
}
