package nodes

import (
	"fmt"
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
	return status
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
