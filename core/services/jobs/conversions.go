package jobs

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/dbutil"
	"github.com/mudler/xlog"
)

// ConvertTaskToRecord converts a schema.Task to a TaskRecord for DB storage.
func ConvertTaskToRecord(task schema.Task, userID ...string) *TaskRecord {
	cronParamsJSON := dbutil.MarshalJSON(task.CronParameters)
	webhooksJSON := dbutil.MarshalJSON(task.Webhooks)

	uid := ""
	if len(userID) > 0 {
		uid = userID[0]
	}

	return &TaskRecord{
		ID:                 task.ID,
		UserID:             uid,
		Name:               task.Name,
		Description:        task.Description,
		Model:              task.Model,
		Prompt:             task.Prompt,
		Enabled:            task.Enabled,
		Cron:               task.Cron,
		CronParametersJSON: cronParamsJSON,
		WebhooksJSON:       webhooksJSON,
		CreatedAt:          task.CreatedAt,
		UpdatedAt:          task.UpdatedAt,
	}
}

// ConvertRecordToTask converts a TaskRecord back to a schema.Task.
func ConvertRecordToTask(rec TaskRecord) schema.Task {
	task := schema.Task{
		ID: rec.ID,
		// UserID not in schema.Task/Job — stored only in DB record
		Name:        rec.Name,
		Description: rec.Description,
		Model:       rec.Model,
		Prompt:      rec.Prompt,
		Enabled:     rec.Enabled,
		Cron:        rec.Cron,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
	if err := dbutil.UnmarshalJSON(rec.CronParametersJSON, &task.CronParameters); err != nil {
		xlog.Warn("Failed to unmarshal task cron parameters", "task_id", rec.ID, "error", err)
	}
	if err := dbutil.UnmarshalJSON(rec.WebhooksJSON, &task.Webhooks); err != nil {
		xlog.Warn("Failed to unmarshal task webhooks", "task_id", rec.ID, "error", err)
	}
	return task
}

// ConvertJobToRecord converts a schema.Job to a JobRecord for DB storage.
func ConvertJobToRecord(job schema.Job, userID ...string) *JobRecord {
	paramsJSON := dbutil.MarshalJSON(job.Parameters)
	imagesJSON := dbutil.MarshalJSON(job.Images)
	videosJSON := dbutil.MarshalJSON(job.Videos)
	audiosJSON := dbutil.MarshalJSON(job.Audios)
	filesJSON := dbutil.MarshalJSON(job.Files)
	tracesJSON := dbutil.MarshalJSON(job.Traces)

	uid := ""
	if len(userID) > 0 {
		uid = userID[0]
	}

	return &JobRecord{
		ID:             job.ID,
		TaskID:         job.TaskID,
		UserID:         uid,
		Status:         string(job.Status),
		ParametersJSON: paramsJSON,
		Result:         job.Result,
		Error:          job.Error,
		TriggeredBy:    job.TriggeredBy,
		ImagesJSON:     imagesJSON,
		VideosJSON:     videosJSON,
		AudiosJSON:     audiosJSON,
		FilesJSON:      filesJSON,
		TracesJSON:     tracesJSON,
		CreatedAt:      job.CreatedAt,
		StartedAt:      job.StartedAt,
		CompletedAt:    job.CompletedAt,
	}
}

// ConvertRecordToJob converts a JobRecord back to a schema.Job.
func ConvertRecordToJob(rec JobRecord) schema.Job {
	job := schema.Job{
		ID:     rec.ID,
		TaskID: rec.TaskID,
		// UserID not in schema.Job — stored only in DB record
		Status:      schema.JobStatus(rec.Status),
		Result:      rec.Result,
		Error:       rec.Error,
		TriggeredBy: rec.TriggeredBy,
		CreatedAt:   rec.CreatedAt,
		StartedAt:   rec.StartedAt,
		CompletedAt: rec.CompletedAt,
	}
	if err := dbutil.UnmarshalJSON(rec.ParametersJSON, &job.Parameters); err != nil {
		xlog.Warn("Failed to unmarshal job parameters", "job_id", rec.ID, "error", err)
	}
	if err := dbutil.UnmarshalJSON(rec.ImagesJSON, &job.Images); err != nil {
		xlog.Warn("Failed to unmarshal job images", "job_id", rec.ID, "error", err)
	}
	if err := dbutil.UnmarshalJSON(rec.VideosJSON, &job.Videos); err != nil {
		xlog.Warn("Failed to unmarshal job videos", "job_id", rec.ID, "error", err)
	}
	if err := dbutil.UnmarshalJSON(rec.AudiosJSON, &job.Audios); err != nil {
		xlog.Warn("Failed to unmarshal job audios", "job_id", rec.ID, "error", err)
	}
	if err := dbutil.UnmarshalJSON(rec.FilesJSON, &job.Files); err != nil {
		xlog.Warn("Failed to unmarshal job files", "job_id", rec.ID, "error", err)
	}
	if err := dbutil.UnmarshalJSON(rec.TracesJSON, &job.Traces); err != nil {
		xlog.Warn("Failed to unmarshal job traces", "job_id", rec.ID, "error", err)
	}
	return job
}
