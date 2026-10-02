package jobs

import (
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// PublishJobResult broadcasts a terminal job result and the matching progress
// event.
//
// Both are FAN-OUT copies. The answer itself reaches the frontend on the reply
// line of the control RPC the claiming replica is reading, and is persisted
// before that claim is released, so neither of these publishes failing can lose
// a job its result. Errors are logged rather than returned for exactly that
// reason: a caller that treated a publish failure as the work having failed
// would report a job that ran as a job that did not.
func PublishJobResult(pub messaging.Publisher, jobID, status, result, errMsg string) {
	if err := pub.Publish(messaging.SubjectJobResult(jobID), JobResultEvent{
		JobID:  jobID,
		Status: status,
		Result: result,
		Error:  errMsg,
	}); err != nil {
		xlog.Error("Failed to publish job result", "jobID", jobID, "error", err)
	}
	if err := pub.Publish(messaging.SubjectJobProgress(jobID), ProgressEvent{
		JobID:   jobID,
		Status:  status,
		Message: errMsg,
	}); err != nil {
		xlog.Error("Failed to publish job progress", "jobID", jobID, "error", err)
	}
}

// PublishJobProgress broadcasts a status-only update, with no result attached.
func PublishJobProgress(pub messaging.Publisher, jobID, status, message string) {
	if err := pub.Publish(messaging.SubjectJobProgress(jobID), ProgressEvent{
		JobID:   jobID,
		Status:  status,
		Message: message,
	}); err != nil {
		xlog.Error("Failed to publish job progress", "jobID", jobID, "error", err)
	}
}
