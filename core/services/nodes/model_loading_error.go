package nodes

import (
	"fmt"
	"math"
	"time"

	"github.com/mudler/LocalAI/core/schema"
)

const (
	// retryAfterFloor and retryAfterCeiling clamp the Retry-After we hand a
	// client. Below the floor a client hammers a load that cannot possibly be
	// done yet; above the ceiling it stops polling long enough that a model
	// which became ready in the meantime sits idle.
	retryAfterFloor   = 5 * time.Second
	retryAfterCeiling = 300 * time.Second
)

// ModelLoadingError reports that the request's model is still cold-loading and
// the caller's wait budget ran out. It carries live progress so the answer is
// actionable — "staging to nvidia-thor, 41%, ETA ~11m" — rather than an
// anonymous timeout, which is what every UI retry produced before.
type ModelLoadingError struct {
	Status     schema.ModelLoadingStatus
	RetryAfter time.Duration
}

func (e *ModelLoadingError) Error() string {
	if e.Status.State == LoadJobStateFailed {
		// A model held by a failed attempt: report the real cause. The caller
		// may retry once the hold ends.
		return fmt.Sprintf("loading model %s: %s", e.Status.Model, e.Status.LastError)
	}
	msg := fmt.Sprintf("model %s is %s", e.Status.Model, e.Status.State)
	if e.Status.Node != "" {
		msg += " on node " + e.Status.Node
	}
	if e.Status.Progress > 0 {
		msg += fmt.Sprintf(" (%.0f%%", e.Status.Progress)
		if e.Status.ETASeconds > 0 {
			msg += fmt.Sprintf(", ETA ~%s", (time.Duration(e.Status.ETASeconds) * time.Second).Round(time.Minute))
		}
		msg += ")"
	}
	return msg
}

// LoadingStatus renders a job row as the API's `loading` object.
func LoadingStatus(job *ModelLoadJob) schema.ModelLoadingStatus {
	status := schema.ModelLoadingStatus{
		Model:      job.TrackingKey,
		State:      job.State,
		Node:       job.NodeName,
		Progress:   job.Progress(),
		BytesSent:  job.BytesSent,
		TotalBytes: job.TotalBytes,
		FileIndex:  job.FileIndex,
		TotalFiles: job.TotalFiles,
	}
	if eta, ok := job.ETA(time.Now()); ok {
		status.ETASeconds = int(eta.Seconds())
	}
	now := time.Now()
	status.JobID = job.Generation
	status.CancelRequested = job.CancelRequested
	status.LastError = job.LastError
	if job.LeaseUntil != nil && job.State != LoadJobStateFailed {
		secs := int(job.LeaseUntil.Sub(now).Seconds())
		status.LeaseExpiresIn = &secs
	}
	if job.State == LoadJobStateFailed {
		status.Stopping = !job.OpConfirmed
		if job.StopDeadline != nil {
			deadline := *job.StopDeadline
			status.StopDeadline = &deadline
			status.RetryAfter = max(int(math.Ceil(deadline.Sub(now).Seconds())), 0)
		}
	}
	return status
}

// NewLoadHeldError is the answer for a request that finds its model held by a
// failed attempt: 503 with the real cause and a Retry-After that says when the
// hold ends. A hold always ends, so the answer is "try again", never "broken".
func NewLoadHeldError(job *ModelLoadJob) *ModelLoadingError {
	status := LoadingStatus(job)
	retryAfter := time.Duration(status.RetryAfter) * time.Second
	retryAfter = min(max(retryAfter, time.Second), retryAfterCeiling)
	return &ModelLoadingError{Status: status, RetryAfter: retryAfter}
}

// newModelLoadingError builds the 503 answer for a caller whose wait budget
// expired. Retry-After is the ETA when the job has one, clamped so it stays a
// useful poll interval, and the caller's own budget otherwise.
func newModelLoadingError(job *ModelLoadJob, budget time.Duration) *ModelLoadingError {
	status := LoadingStatus(job)
	retryAfter := budget
	if status.ETASeconds > 0 {
		retryAfter = time.Duration(status.ETASeconds) * time.Second
	}
	retryAfter = min(max(retryAfter, retryAfterFloor), retryAfterCeiling)
	return &ModelLoadingError{Status: status, RetryAfter: retryAfter}
}
