package jobs

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
)

var _ = Describe("Conversions", func() {
	Describe("ConvertTask round-trip", func() {
		It("preserves all fields", func() {
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

			Expect(result.ID).To(Equal(original.ID))
			Expect(result.Name).To(Equal(original.Name))
			Expect(result.Description).To(Equal(original.Description))
			Expect(result.Model).To(Equal(original.Model))
			Expect(result.Prompt).To(Equal(original.Prompt))
			Expect(result.Enabled).To(Equal(original.Enabled))
			Expect(result.Cron).To(Equal(original.Cron))
			Expect(result.CronParameters).To(Equal(original.CronParameters))
			Expect(result.Webhooks).To(Equal(original.Webhooks))
			Expect(result.CreatedAt).To(BeTemporally("~", original.CreatedAt, time.Second))
			Expect(result.UpdatedAt).To(BeTemporally("~", original.UpdatedAt, time.Second))
		})

		It("handles empty fields", func() {
			original := schema.Task{
				ID:   "task-empty",
				Name: "empty-task",
			}

			rec := ConvertTaskToRecord(original)
			result := ConvertRecordToTask(*rec)

			Expect(result.ID).To(Equal(original.ID))
			Expect(result.CronParameters).To(BeNil())
			Expect(result.Webhooks).To(BeNil())
		})
	})

	Describe("ConvertJob round-trip", func() {
		It("preserves all fields", func() {
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

			Expect(result.ID).To(Equal(original.ID))
			Expect(result.TaskID).To(Equal(original.TaskID))
			Expect(result.Status).To(Equal(original.Status))
			Expect(result.Parameters).To(Equal(original.Parameters))
			Expect(result.Result).To(Equal(original.Result))
			Expect(result.TriggeredBy).To(Equal(original.TriggeredBy))
			Expect(result.Images).To(Equal(original.Images))
			Expect(result.Videos).To(Equal(original.Videos))
			Expect(result.Audios).To(Equal(original.Audios))
			Expect(result.Files).To(Equal(original.Files))
			Expect(result.Traces).To(HaveLen(len(original.Traces)))
			Expect(result.Traces[0].Type).To(Equal(original.Traces[0].Type))
			Expect(result.Traces[0].ToolName).To(Equal(original.Traces[0].ToolName))
			Expect(result.StartedAt).ToNot(BeNil())
			Expect(result.StartedAt.Truncate(time.Second)).To(Equal(startedAt))
			Expect(result.CompletedAt).ToNot(BeNil())
			Expect(result.CompletedAt.Truncate(time.Second)).To(Equal(completedAt))
		})

		It("handles empty fields", func() {
			original := schema.Job{
				ID:     "job-empty",
				TaskID: "task-empty",
				Status: schema.JobStatusPending,
			}

			rec := ConvertJobToRecord(original)
			result := ConvertRecordToJob(*rec)

			Expect(result.ID).To(Equal(original.ID))
			Expect(result.Parameters).To(BeNil())
			Expect(result.Images).To(BeNil())
			Expect(result.Videos).To(BeNil())
			Expect(result.Audios).To(BeNil())
			Expect(result.Files).To(BeNil())
			Expect(result.Traces).To(BeNil())
		})

		It("handles malformed JSON gracefully", func() {
			rec := JobRecord{
				ID:             "job-bad-json",
				TaskID:         "task-1",
				Status:         "pending",
				ParametersJSON: "not json",
				ImagesJSON:     "not json",
				TracesJSON:     "{bad}",
			}

			result := ConvertRecordToJob(rec)

			Expect(result.ID).To(Equal("job-bad-json"))
			Expect(result.Parameters).To(BeNil())
			Expect(result.Images).To(BeNil())
			Expect(result.Traces).To(BeNil())
		})
	})
})
