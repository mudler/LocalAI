package jobs

import (
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

// ConvertTaskToRecord converts a schema.Task to a TaskRecord for DB storage.
func ConvertTaskToRecord(task schema.Task, userID ...string) *TaskRecord {
	var cronParamsJSON, webhooksJSON string
	if task.CronParameters != nil {
		data, _ := json.Marshal(task.CronParameters)
		cronParamsJSON = string(data)
	}
	if task.Webhooks != nil {
		data, _ := json.Marshal(task.Webhooks)
		webhooksJSON = string(data)
	}

	uid := ""
	if len(userID) > 0 {
		uid = userID[0]
	}

	return &TaskRecord{
		ID:                 task.ID,
		UserID:             uid,
		Name:               task.Name,
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
		ID:        rec.ID,
		// UserID not in schema.Task/Job — stored only in DB record
		Name:      rec.Name,
		Model:     rec.Model,
		Prompt:    rec.Prompt,
		Enabled:   rec.Enabled,
		Cron:      rec.Cron,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
	if rec.CronParametersJSON != "" {
		if err := json.Unmarshal([]byte(rec.CronParametersJSON), &task.CronParameters); err != nil {
			xlog.Warn("Failed to unmarshal task cron parameters", "task_id", rec.ID, "error", err)
		}
	}
	if rec.WebhooksJSON != "" {
		if err := json.Unmarshal([]byte(rec.WebhooksJSON), &task.Webhooks); err != nil {
			xlog.Warn("Failed to unmarshal task webhooks", "task_id", rec.ID, "error", err)
		}
	}
	return task
}

// ConvertJobToRecord converts a schema.Job to a JobRecord for DB storage.
func ConvertJobToRecord(job schema.Job, userID ...string) *JobRecord {
	var paramsJSON, imagesJSON, videosJSON, audiosJSON, filesJSON, tracesJSON string
	if job.Parameters != nil {
		data, _ := json.Marshal(job.Parameters)
		paramsJSON = string(data)
	}
	if job.Images != nil {
		data, _ := json.Marshal(job.Images)
		imagesJSON = string(data)
	}
	if job.Videos != nil {
		data, _ := json.Marshal(job.Videos)
		videosJSON = string(data)
	}
	if job.Audios != nil {
		data, _ := json.Marshal(job.Audios)
		audiosJSON = string(data)
	}
	if job.Files != nil {
		data, _ := json.Marshal(job.Files)
		filesJSON = string(data)
	}
	if job.Traces != nil {
		data, _ := json.Marshal(job.Traces)
		tracesJSON = string(data)
	}

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
		ID:          rec.ID,
		TaskID:      rec.TaskID,
		// UserID not in schema.Job — stored only in DB record
		Status:      schema.JobStatus(rec.Status),
		Result:      rec.Result,
		Error:       rec.Error,
		TriggeredBy: rec.TriggeredBy,
		CreatedAt:   rec.CreatedAt,
		StartedAt:   rec.StartedAt,
		CompletedAt: rec.CompletedAt,
	}
	if rec.ParametersJSON != "" {
		if err := json.Unmarshal([]byte(rec.ParametersJSON), &job.Parameters); err != nil {
			xlog.Warn("Failed to unmarshal job parameters", "job_id", rec.ID, "error", err)
		}
	}
	if rec.ImagesJSON != "" {
		if err := json.Unmarshal([]byte(rec.ImagesJSON), &job.Images); err != nil {
			xlog.Warn("Failed to unmarshal job images", "job_id", rec.ID, "error", err)
		}
	}
	if rec.VideosJSON != "" {
		if err := json.Unmarshal([]byte(rec.VideosJSON), &job.Videos); err != nil {
			xlog.Warn("Failed to unmarshal job videos", "job_id", rec.ID, "error", err)
		}
	}
	if rec.AudiosJSON != "" {
		if err := json.Unmarshal([]byte(rec.AudiosJSON), &job.Audios); err != nil {
			xlog.Warn("Failed to unmarshal job audios", "job_id", rec.ID, "error", err)
		}
	}
	if rec.FilesJSON != "" {
		if err := json.Unmarshal([]byte(rec.FilesJSON), &job.Files); err != nil {
			xlog.Warn("Failed to unmarshal job files", "job_id", rec.ID, "error", err)
		}
	}
	if rec.TracesJSON != "" {
		if err := json.Unmarshal([]byte(rec.TracesJSON), &job.Traces); err != nil {
			xlog.Warn("Failed to unmarshal job traces", "job_id", rec.ID, "error", err)
		}
	}
	return job
}

// TimeOrNow returns the given time if non-zero, otherwise returns now.
func TimeOrNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
