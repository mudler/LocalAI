package agentpool

import (
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/jobs"
)

// dbJobPersister persists tasks and jobs to PostgreSQL via JobStore.
// It provides authoritative reads (GetJob/ListJobs) since NATS result
// events update the DB directly, bypassing the in-memory map.
type dbJobPersister struct {
	store *jobs.JobStore
}

func (p *dbJobPersister) SaveTask(userID string, task schema.Task) error {
	rec := jobs.ConvertTaskToRecord(task, userID)
	return p.store.SaveTask(rec)
}

func (p *dbJobPersister) DeleteTask(taskID string) error {
	return p.store.DeleteTask(taskID)
}

func (p *dbJobPersister) SaveJob(userID string, job schema.Job) error {
	rec := jobs.ConvertJobToRecord(job, userID)
	return p.store.SaveJob(rec)
}

func (p *dbJobPersister) DeleteJob(jobID string) error {
	return p.store.DeleteJob(jobID)
}

func (p *dbJobPersister) GetJob(jobID string) (*schema.Job, error) {
	rec, err := p.store.GetJob(jobID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	job := jobs.ConvertRecordToJob(*rec)
	return &job, nil
}

func (p *dbJobPersister) ListJobs(userID, taskID, status string, limit int) ([]schema.Job, error) {
	recs, err := p.store.ListJobs(userID, taskID, status, limit)
	if err != nil {
		return nil, err
	}
	result := make([]schema.Job, 0, len(recs))
	for _, rec := range recs {
		result = append(result, jobs.ConvertRecordToJob(rec))
	}
	return result, nil
}

func (p *dbJobPersister) LoadTasks(userID string) ([]schema.Task, error) {
	recs, err := p.store.ListTasks(userID)
	if err != nil {
		return nil, err
	}
	result := make([]schema.Task, 0, len(recs))
	for _, rec := range recs {
		result = append(result, jobs.ConvertRecordToTask(rec))
	}
	return result, nil
}

func (p *dbJobPersister) LoadJobs(userID string) ([]schema.Job, error) {
	recs, err := p.store.ListJobs(userID, "", "", 0)
	if err != nil {
		return nil, err
	}
	result := make([]schema.Job, 0, len(recs))
	for _, rec := range recs {
		result = append(result, jobs.ConvertRecordToJob(rec))
	}
	return result, nil
}

func (p *dbJobPersister) CleanupOldJobs(retention time.Duration) (int64, error) {
	return p.store.CleanupOldJobs(retention)
}
