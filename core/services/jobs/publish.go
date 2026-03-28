package jobs

import (
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// PublishJobResult publishes a terminal job result event and a progress event via NATS.
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

// PublishJobProgress publishes a status-only update (no result) via NATS.
func PublishJobProgress(pub messaging.Publisher, jobID, status, message string) {
	if err := pub.Publish(messaging.SubjectJobResult(jobID), JobResultEvent{
		JobID:  jobID,
		Status: status,
	}); err != nil {
		xlog.Error("Failed to publish job result", "jobID", jobID, "error", err)
	}
	if err := pub.Publish(messaging.SubjectJobProgress(jobID), ProgressEvent{
		JobID:   jobID,
		Status:  status,
		Message: message,
	}); err != nil {
		xlog.Error("Failed to publish job progress", "jobID", jobID, "error", err)
	}
}
