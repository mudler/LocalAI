package jobs

import (
	"reflect"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/schema"
)

func TestConvertTaskRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	original := schema.Task{
		ID:          "task-123",
		Name:        "my-task",
		Description: "a test task",
		Model:       "gpt-4",
		Prompt:      "hello {{.name}}",
		CreatedAt:   now,
		UpdatedAt:   now,
		Enabled:     true,
		Cron:        "*/5 * * * *",
		CronParameters: map[string]string{
			"name":  "world",
			"count": "42",
		},
		Webhooks: []schema.WebhookConfig{
			{
				URL:    "https://example.com/hook",
				Method: "POST",
				Headers: map[string]string{
					"Authorization": "Bearer token",
				},
			},
		},
	}

	rec := ConvertTaskToRecord(original, "user-1")
	result := ConvertRecordToTask(*rec)

	// Description is not round-tripped (not in ConvertRecordToTask)
	// so compare individual fields that are preserved.
	if result.ID != original.ID {
		t.Errorf("ID: got %q, want %q", result.ID, original.ID)
	}
	if result.Name != original.Name {
		t.Errorf("Name: got %q, want %q", result.Name, original.Name)
	}
	if result.Model != original.Model {
		t.Errorf("Model: got %q, want %q", result.Model, original.Model)
	}
	if result.Prompt != original.Prompt {
		t.Errorf("Prompt: got %q, want %q", result.Prompt, original.Prompt)
	}
	if result.Enabled != original.Enabled {
		t.Errorf("Enabled: got %v, want %v", result.Enabled, original.Enabled)
	}
	if result.Cron != original.Cron {
		t.Errorf("Cron: got %q, want %q", result.Cron, original.Cron)
	}
	if !reflect.DeepEqual(result.CronParameters, original.CronParameters) {
		t.Errorf("CronParameters: got %v, want %v", result.CronParameters, original.CronParameters)
	}
	if !reflect.DeepEqual(result.Webhooks, original.Webhooks) {
		t.Errorf("Webhooks: got %v, want %v", result.Webhooks, original.Webhooks)
	}
	if !result.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", result.CreatedAt, original.CreatedAt)
	}
	if !result.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("UpdatedAt: got %v, want %v", result.UpdatedAt, original.UpdatedAt)
	}
}

func TestConvertTaskEmptyFields(t *testing.T) {
	original := schema.Task{
		ID:   "task-empty",
		Name: "empty-task",
	}

	rec := ConvertTaskToRecord(original)
	result := ConvertRecordToTask(*rec)

	if result.ID != original.ID {
		t.Errorf("ID: got %q, want %q", result.ID, original.ID)
	}
	if result.CronParameters != nil {
		t.Errorf("CronParameters: got %v, want nil", result.CronParameters)
	}
	if result.Webhooks != nil {
		t.Errorf("Webhooks: got %v, want nil", result.Webhooks)
	}
}

func TestConvertJobRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	startedAt := now.Add(-time.Minute)
	completedAt := now

	original := schema.Job{
		ID:          "job-456",
		TaskID:      "task-123",
		Status:      schema.JobStatusCompleted,
		Parameters:  map[string]string{"key": "value", "foo": "bar"},
		Result:      "success result",
		Error:       "",
		TriggeredBy: "manual",
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		CreatedAt:   now,
		Traces: []schema.JobTrace{
			{
				Type:      "tool_call",
				Content:   "called search",
				Timestamp: now,
				ToolName:  "search",
			},
		},
		Images: []string{"img1.png", "img2.png"},
		Videos: []string{"vid1.mp4"},
		Audios: []string{"aud1.mp3"},
		Files:  []string{"file1.txt"},
	}

	rec := ConvertJobToRecord(original, "user-1")
	result := ConvertRecordToJob(*rec)

	if result.ID != original.ID {
		t.Errorf("ID: got %q, want %q", result.ID, original.ID)
	}
	if result.TaskID != original.TaskID {
		t.Errorf("TaskID: got %q, want %q", result.TaskID, original.TaskID)
	}
	if result.Status != original.Status {
		t.Errorf("Status: got %q, want %q", result.Status, original.Status)
	}
	if !reflect.DeepEqual(result.Parameters, original.Parameters) {
		t.Errorf("Parameters: got %v, want %v", result.Parameters, original.Parameters)
	}
	if result.Result != original.Result {
		t.Errorf("Result: got %q, want %q", result.Result, original.Result)
	}
	if result.TriggeredBy != original.TriggeredBy {
		t.Errorf("TriggeredBy: got %q, want %q", result.TriggeredBy, original.TriggeredBy)
	}
	if !reflect.DeepEqual(result.Images, original.Images) {
		t.Errorf("Images: got %v, want %v", result.Images, original.Images)
	}
	if !reflect.DeepEqual(result.Videos, original.Videos) {
		t.Errorf("Videos: got %v, want %v", result.Videos, original.Videos)
	}
	if !reflect.DeepEqual(result.Audios, original.Audios) {
		t.Errorf("Audios: got %v, want %v", result.Audios, original.Audios)
	}
	if !reflect.DeepEqual(result.Files, original.Files) {
		t.Errorf("Files: got %v, want %v", result.Files, original.Files)
	}
	if len(result.Traces) != len(original.Traces) {
		t.Fatalf("Traces length: got %d, want %d", len(result.Traces), len(original.Traces))
	}
	if result.Traces[0].Type != original.Traces[0].Type {
		t.Errorf("Traces[0].Type: got %q, want %q", result.Traces[0].Type, original.Traces[0].Type)
	}
	if result.Traces[0].ToolName != original.Traces[0].ToolName {
		t.Errorf("Traces[0].ToolName: got %q, want %q", result.Traces[0].ToolName, original.Traces[0].ToolName)
	}
	if result.StartedAt == nil || !result.StartedAt.Truncate(time.Second).Equal(startedAt) {
		t.Errorf("StartedAt: got %v, want %v", result.StartedAt, startedAt)
	}
	if result.CompletedAt == nil || !result.CompletedAt.Truncate(time.Second).Equal(completedAt) {
		t.Errorf("CompletedAt: got %v, want %v", result.CompletedAt, completedAt)
	}
}

func TestConvertJobEmptyFields(t *testing.T) {
	original := schema.Job{
		ID:     "job-empty",
		TaskID: "task-empty",
		Status: schema.JobStatusPending,
	}

	rec := ConvertJobToRecord(original)
	result := ConvertRecordToJob(*rec)

	if result.ID != original.ID {
		t.Errorf("ID: got %q, want %q", result.ID, original.ID)
	}
	if result.Parameters != nil {
		t.Errorf("Parameters: got %v, want nil", result.Parameters)
	}
	if result.Images != nil {
		t.Errorf("Images: got %v, want nil", result.Images)
	}
	if result.Videos != nil {
		t.Errorf("Videos: got %v, want nil", result.Videos)
	}
	if result.Audios != nil {
		t.Errorf("Audios: got %v, want nil", result.Audios)
	}
	if result.Files != nil {
		t.Errorf("Files: got %v, want nil", result.Files)
	}
	if result.Traces != nil {
		t.Errorf("Traces: got %v, want nil", result.Traces)
	}
}

func TestConvertRecordToJobMalformedJSON(t *testing.T) {
	rec := JobRecord{
		ID:             "job-bad-json",
		TaskID:         "task-1",
		Status:         "pending",
		ParametersJSON: "not json",
		ImagesJSON:     "not json",
		TracesJSON:     "{bad}",
	}

	// Should not panic
	result := ConvertRecordToJob(rec)

	if result.ID != "job-bad-json" {
		t.Errorf("ID: got %q, want %q", result.ID, "job-bad-json")
	}
	// Malformed JSON should result in nil (unmarshal fails, field stays zero-value)
	if result.Parameters != nil {
		t.Errorf("Parameters: got %v, want nil", result.Parameters)
	}
	if result.Images != nil {
		t.Errorf("Images: got %v, want nil", result.Images)
	}
	if result.Traces != nil {
		t.Errorf("Traces: got %v, want nil", result.Traces)
	}
}
