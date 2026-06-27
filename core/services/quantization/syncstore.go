package quantization

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/syncstate"
)

// quantStoreAdapter bridges the distributed PostgreSQL QuantStore to the generic
// syncstate.Store the SyncedMap consumes. It is only wired in distributed mode;
// standalone leaves Store nil and hydrates from disk via a Loader instead.
//
// The SyncedMap value type is *schema.QuantizationJob (the exact shape the REST
// API returns) so reads need no conversion and the response JSON is provably
// unchanged. The adapter is the single place that translates between that API
// shape and the DB QuantJobRecord.
type quantStoreAdapter struct {
	store *distributed.QuantStore
}

// compile-time assertion that the adapter satisfies the component's Store.
var _ syncstate.Store[string, *schema.QuantizationJob] = (*quantStoreAdapter)(nil)

func (a *quantStoreAdapter) List(_ context.Context) ([]*schema.QuantizationJob, error) {
	records, err := a.store.ListAll()
	if err != nil {
		return nil, err
	}
	jobs := make([]*schema.QuantizationJob, 0, len(records))
	for i := range records {
		jobs = append(jobs, recordToJob(&records[i]))
	}
	return jobs, nil
}

func (a *quantStoreAdapter) Upsert(_ context.Context, job *schema.QuantizationJob) error {
	return a.store.Upsert(jobToRecord(job))
}

func (a *quantStoreAdapter) Delete(_ context.Context, id string) error {
	return a.store.Delete(id)
}

// recordToJob maps a persisted DB record back to the API shape, reconstructing
// the structured Config / ExtraOptions from their JSON columns.
func recordToJob(r *distributed.QuantJobRecord) *schema.QuantizationJob {
	job := &schema.QuantizationJob{
		ID:               r.ID,
		UserID:           r.UserID,
		Model:            r.Model,
		Backend:          r.Backend,
		ModelID:          r.ModelID,
		QuantizationType: r.QuantizationType,
		Status:           r.Status,
		Message:          r.Message,
		OutputDir:        r.OutputDir,
		OutputFile:       r.OutputFile,
		ImportStatus:     r.ImportStatus,
		ImportMessage:    r.ImportMessage,
		ImportModelName:  r.ImportModelName,
		CreatedAt:        r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.ExtraOptsJSON != "" {
		// Best-effort: a malformed column must not drop the whole job from the API.
		_ = json.Unmarshal([]byte(r.ExtraOptsJSON), &job.ExtraOptions)
	}
	if r.ConfigJSON != "" {
		var cfg schema.QuantizationJobRequest
		if err := json.Unmarshal([]byte(r.ConfigJSON), &cfg); err == nil {
			job.Config = &cfg
		}
	}
	return job
}

// jobToRecord maps the API shape to a DB record for write-through, serializing
// the structured Config / ExtraOptions into their JSON columns. CreatedAt is
// parsed back from the RFC3339 string the service stamps; an unparseable value is
// left zero so QuantStore.Upsert stamps "now".
func jobToRecord(job *schema.QuantizationJob) *distributed.QuantJobRecord {
	rec := &distributed.QuantJobRecord{
		ID:               job.ID,
		UserID:           job.UserID,
		Model:            job.Model,
		Backend:          job.Backend,
		ModelID:          job.ModelID,
		QuantizationType: job.QuantizationType,
		Status:           job.Status,
		Message:          job.Message,
		OutputDir:        job.OutputDir,
		OutputFile:       job.OutputFile,
		ImportStatus:     job.ImportStatus,
		ImportMessage:    job.ImportMessage,
		ImportModelName:  job.ImportModelName,
	}
	if job.Config != nil {
		if data, err := json.Marshal(job.Config); err == nil {
			rec.ConfigJSON = string(data)
		}
	}
	if job.ExtraOptions != nil {
		if data, err := json.Marshal(job.ExtraOptions); err == nil {
			rec.ExtraOptsJSON = string(data)
		}
	}
	if t, err := time.Parse(time.RFC3339, job.CreatedAt); err == nil {
		rec.CreatedAt = t
	}
	return rec
}
