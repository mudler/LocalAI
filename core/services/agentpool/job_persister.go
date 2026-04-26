package agentpool

import (
	"time"

	"github.com/mudler/LocalAI/core/schema"
)

// JobPersister abstracts task/job persistence between file-backed and DB-backed modes.
// The in-memory syncmap remains the primary store; the persister provides secondary
// persistence and authoritative reads (DB mode only).
type JobPersister interface {
	// Write-through persistence (called after in-memory mutation)
	SaveTask(userID string, task schema.Task) error
	DeleteTask(taskID string) error
	SaveJob(userID string, job schema.Job) error
	DeleteJob(jobID string) error

	// Authoritative reads — DB returns fresh data; file returns nil, nil
	GetJob(jobID string) (*schema.Job, error)
	ListJobs(userID, taskID, status string, limit int) ([]schema.Job, error)

	// Bootstrap (load all persisted data at startup)
	LoadTasks(userID string) ([]schema.Task, error)
	LoadJobs(userID string) ([]schema.Job, error)

	// Maintenance
	CleanupOldJobs(retention time.Duration) (int64, error)
}
