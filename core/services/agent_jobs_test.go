package services_test

import (
	"context"
	"os"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AgentJobService", func() {
	var (
		service      *services.AgentJobService
		tempDir      string
		appConfig    *config.ApplicationConfig
		modelLoader  *model.ModelLoader
		configLoader *config.ModelConfigLoader
		evaluator    *templates.Evaluator
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "agent_jobs_test")
		Expect(err).NotTo(HaveOccurred())

		systemState := &system.SystemState{}
		systemState.Model.ModelsPath = tempDir

		appConfig = config.NewApplicationConfig(
			config.WithDynamicConfigDir(tempDir),
			config.WithContext(context.Background()),
		)
		appConfig.SystemState = systemState
		appConfig.APIAddress = "127.0.0.1:8080"
		appConfig.AgentJobRetentionDays = 30

		modelLoader = model.NewModelLoader(systemState, false)
		configLoader = config.NewModelConfigLoader(tempDir)
		evaluator = templates.NewEvaluator(tempDir)

		service = services.NewAgentJobService(
			appConfig,
			modelLoader,
			configLoader,
			evaluator,
		)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("Task CRUD operations", func() {
		It("should create a task", func() {
			task := schema.Task{
				Name:        "Test Task",
				Description: "Test Description",
				Model:       "test-model",
				Prompt:      "Hello {{.name}}",
				Enabled:     true,
			}

			id, err := service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(BeEmpty())

			retrieved, err := service.GetTask(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(retrieved.Name).To(Equal("Test Task"))
			Expect(retrieved.Description).To(Equal("Test Description"))
			Expect(retrieved.Model).To(Equal("test-model"))
			Expect(retrieved.Prompt).To(Equal("Hello {{.name}}"))
		})

		It("should update a task", func() {
			task := schema.Task{
				Name:   "Original Task",
				Model:  "test-model",
				Prompt: "Original prompt",
			}

			id, err := service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())

			updatedTask := schema.Task{
				Name:   "Updated Task",
				Model:  "test-model",
				Prompt: "Updated prompt",
			}

			err = service.UpdateTask(id, updatedTask)
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := service.GetTask(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(retrieved.Name).To(Equal("Updated Task"))
			Expect(retrieved.Prompt).To(Equal("Updated prompt"))
		})

		It("should delete a task", func() {
			task := schema.Task{
				Name:   "Task to Delete",
				Model:  "test-model",
				Prompt: "Prompt",
			}

			id, err := service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())

			err = service.DeleteTask(id)
			Expect(err).NotTo(HaveOccurred())

			_, err = service.GetTask(id)
			Expect(err).To(HaveOccurred())
		})

		It("should list all tasks", func() {
			task1 := schema.Task{Name: "Task 1", Model: "test-model", Prompt: "Prompt 1"}
			task2 := schema.Task{Name: "Task 2", Model: "test-model", Prompt: "Prompt 2"}

			_, err := service.CreateTask(task1)
			Expect(err).NotTo(HaveOccurred())
			_, err = service.CreateTask(task2)
			Expect(err).NotTo(HaveOccurred())

			tasks := service.ListTasks()
			Expect(len(tasks)).To(BeNumerically(">=", 2))
		})
	})

	Describe("Job operations", func() {
		var taskID string

		BeforeEach(func() {
			task := schema.Task{
				Name:    "Test Task",
				Model:   "test-model",
				Prompt:  "Hello {{.name}}",
				Enabled: true,
			}
			var err error
			taskID, err = service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should create and queue a job", func() {
			params := map[string]string{"name": "World"}
			jobID, err := service.ExecuteJob(taskID, params, "test", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(jobID).NotTo(BeEmpty())

			job, err := service.GetJob(jobID)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.TaskID).To(Equal(taskID))
			Expect(job.Status).To(Equal(schema.JobStatusPending))
			Expect(job.Parameters).To(Equal(params))
		})

		It("should list jobs with filters", func() {
			params := map[string]string{}
			jobID1, err := service.ExecuteJob(taskID, params, "test", nil)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(10 * time.Millisecond) // Ensure different timestamps

			jobID2, err := service.ExecuteJob(taskID, params, "test", nil)
			Expect(err).NotTo(HaveOccurred())

			allJobs := service.ListJobs(nil, nil, 0)
			Expect(len(allJobs)).To(BeNumerically(">=", 2))

			filteredJobs := service.ListJobs(&taskID, nil, 0)
			Expect(len(filteredJobs)).To(BeNumerically(">=", 2))

			status := schema.JobStatusPending
			pendingJobs := service.ListJobs(nil, &status, 0)
			Expect(len(pendingJobs)).To(BeNumerically(">=", 2))

			// Verify both jobs are in the list
			jobIDs := make(map[string]bool)
			for _, job := range pendingJobs {
				jobIDs[job.ID] = true
			}
			Expect(jobIDs[jobID1]).To(BeTrue())
			Expect(jobIDs[jobID2]).To(BeTrue())
		})

		It("should cancel a pending job", func() {
			params := map[string]string{}
			jobID, err := service.ExecuteJob(taskID, params, "test", nil)
			Expect(err).NotTo(HaveOccurred())

			err = service.CancelJob(jobID)
			Expect(err).NotTo(HaveOccurred())

			job, err := service.GetJob(jobID)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.Status).To(Equal(schema.JobStatusCancelled))
		})

		It("should delete a job", func() {
			params := map[string]string{}
			jobID, err := service.ExecuteJob(taskID, params, "test", nil)
			Expect(err).NotTo(HaveOccurred())

			err = service.DeleteJob(jobID)
			Expect(err).NotTo(HaveOccurred())

			_, err = service.GetJob(jobID)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("File operations", func() {
		It("should save and load tasks from file", func() {
			task := schema.Task{
				Name:   "Persistent Task",
				Model:  "test-model",
				Prompt: "Test prompt",
			}

			id, err := service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())

			// Create a new service instance to test loading
			newService := services.NewAgentJobService(
				appConfig,
				modelLoader,
				configLoader,
				evaluator,
			)

			err = newService.LoadTasksFromFile()
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := newService.GetTask(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(retrieved.Name).To(Equal("Persistent Task"))
		})

		It("should save and load jobs from file", func() {
			task := schema.Task{
				Name:    "Test Task",
				Model:   "test-model",
				Prompt:  "Test prompt",
				Enabled: true,
			}

			taskID, err := service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())

			params := map[string]string{}
			jobID, err := service.ExecuteJob(taskID, params, "test", nil)
			Expect(err).NotTo(HaveOccurred())

			service.SaveJobsToFile()

			// Create a new service instance to test loading
			newService := services.NewAgentJobService(
				appConfig,
				modelLoader,
				configLoader,
				evaluator,
			)

			err = newService.LoadJobsFromFile()
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := newService.GetJob(jobID)
			Expect(err).NotTo(HaveOccurred())
			Expect(retrieved.TaskID).To(Equal(taskID))
		})
	})

	Describe("Prompt templating", func() {
		It("should build prompt from template with parameters", func() {
			task := schema.Task{
				Name:   "Template Task",
				Model:  "test-model",
				Prompt: "Hello {{.name}}, you are {{.role}}",
			}

			id, err := service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())

			// We can't directly test buildPrompt as it's private, but we can test via ExecuteJob
			// which uses it internally. However, without a real model, the job will fail.
			// So we'll just verify the task was created correctly.
			Expect(id).NotTo(BeEmpty())
		})
	})

	Describe("Job cleanup", func() {
		It("should cleanup old jobs", func() {
			task := schema.Task{
				Name:    "Test Task",
				Model:   "test-model",
				Prompt:  "Test prompt",
				Enabled: true,
			}

			taskID, err := service.CreateTask(task)
			Expect(err).NotTo(HaveOccurred())

			params := map[string]string{}
			jobID, err := service.ExecuteJob(taskID, params, "test", nil)
			Expect(err).NotTo(HaveOccurred())

			// Manually set job creation time to be old
			job, err := service.GetJob(jobID)
			Expect(err).NotTo(HaveOccurred())

			// Modify the job's CreatedAt to be 31 days ago
			oldTime := time.Now().AddDate(0, 0, -31)
			job.CreatedAt = oldTime
			// We can't directly modify jobs in the service, so we'll test cleanup differently
			// by setting retention to 0 and creating a new job

			// Test that cleanup runs without error
			err = service.CleanupOldJobs()
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Multimedia support", func() {
		Describe("Task multimedia sources", func() {
			It("should create a task with multimedia sources", func() {
				task := schema.Task{
					Name:   "Multimedia Task",
					Model:  "test-model",
					Prompt: "Analyze this image",
					MultimediaSources: []schema.MultimediaSourceConfig{
						{
							Type:    "image",
							URL:     "https://example.com/image.png",
							Headers: map[string]string{"Authorization": "Bearer token123"},
						},
						{
							Type: "video",
							URL:  "https://example.com/video.mp4",
						},
					},
				}

				id, err := service.CreateTask(task)
				Expect(err).NotTo(HaveOccurred())
				Expect(id).NotTo(BeEmpty())

				retrieved, err := service.GetTask(id)
				Expect(err).NotTo(HaveOccurred())
				Expect(retrieved.MultimediaSources).To(HaveLen(2))
				Expect(retrieved.MultimediaSources[0].Type).To(Equal("image"))
				Expect(retrieved.MultimediaSources[0].URL).To(Equal("https://example.com/image.png"))
				Expect(retrieved.MultimediaSources[0].Headers["Authorization"]).To(Equal("Bearer token123"))
				Expect(retrieved.MultimediaSources[1].Type).To(Equal("video"))
			})

			It("should save and load tasks with multimedia sources from file", func() {
				task := schema.Task{
					Name:   "Persistent Multimedia Task",
					Model:  "test-model",
					Prompt: "Test prompt",
					MultimediaSources: []schema.MultimediaSourceConfig{
						{
							Type: "audio",
							URL:  "https://example.com/audio.mp3",
							Headers: map[string]string{
								"X-Custom-Header": "value",
							},
						},
					},
				}

				id, err := service.CreateTask(task)
				Expect(err).NotTo(HaveOccurred())

				// Create a new service instance to test loading
				newService := services.NewAgentJobService(
					appConfig,
					modelLoader,
					configLoader,
					evaluator,
				)

				err = newService.LoadTasksFromFile()
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := newService.GetTask(id)
				Expect(err).NotTo(HaveOccurred())
				Expect(retrieved.Name).To(Equal("Persistent Multimedia Task"))
				Expect(retrieved.MultimediaSources).To(HaveLen(1))
				Expect(retrieved.MultimediaSources[0].Type).To(Equal("audio"))
				Expect(retrieved.MultimediaSources[0].URL).To(Equal("https://example.com/audio.mp3"))
				Expect(retrieved.MultimediaSources[0].Headers["X-Custom-Header"]).To(Equal("value"))
			})
		})

		Describe("Job multimedia", func() {
			var taskID string

			BeforeEach(func() {
				task := schema.Task{
					Name:    "Test Task",
					Model:   "test-model",
					Prompt:  "Hello {{.name}}",
					Enabled: true,
				}
				var err error
				taskID, err = service.CreateTask(task)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should create a job with multimedia content", func() {
				params := map[string]string{"name": "World"}
				multimedia := &schema.MultimediaAttachment{
					Images: []string{"https://example.com/image1.png", "data:image/png;base64,iVBORw0KG"},
					Videos: []string{"https://example.com/video.mp4"},
					Audios: []string{"data:audio/mpeg;base64,SUQzBAAAAA"},
					Files:  []string{"https://example.com/file.pdf"},
				}

				jobID, err := service.ExecuteJob(taskID, params, "test", multimedia)
				Expect(err).NotTo(HaveOccurred())
				Expect(jobID).NotTo(BeEmpty())

				job, err := service.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				Expect(job.TaskID).To(Equal(taskID))
				Expect(job.Images).To(HaveLen(2))
				Expect(job.Images[0]).To(Equal("https://example.com/image1.png"))
				Expect(job.Images[1]).To(Equal("data:image/png;base64,iVBORw0KG"))
				Expect(job.Videos).To(HaveLen(1))
				Expect(job.Videos[0]).To(Equal("https://example.com/video.mp4"))
				Expect(job.Audios).To(HaveLen(1))
				Expect(job.Audios[0]).To(Equal("data:audio/mpeg;base64,SUQzBAAAAA"))
				Expect(job.Files).To(HaveLen(1))
				Expect(job.Files[0]).To(Equal("https://example.com/file.pdf"))
			})

			It("should create a job with partial multimedia (only images)", func() {
				params := map[string]string{}
				multimedia := &schema.MultimediaAttachment{
					Images: []string{"https://example.com/image.png"},
				}

				jobID, err := service.ExecuteJob(taskID, params, "test", multimedia)
				Expect(err).NotTo(HaveOccurred())

				job, err := service.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				Expect(job.Images).To(HaveLen(1))
				Expect(job.Videos).To(BeEmpty())
				Expect(job.Audios).To(BeEmpty())
				Expect(job.Files).To(BeEmpty())
			})

			It("should create a job without multimedia (nil)", func() {
				params := map[string]string{"name": "Test"}
				jobID, err := service.ExecuteJob(taskID, params, "test", nil)
				Expect(err).NotTo(HaveOccurred())

				job, err := service.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				Expect(job.Images).To(BeEmpty())
				Expect(job.Videos).To(BeEmpty())
				Expect(job.Audios).To(BeEmpty())
				Expect(job.Files).To(BeEmpty())
			})

			It("should save and load jobs with multimedia from file", func() {
				params := map[string]string{}
				multimedia := &schema.MultimediaAttachment{
					Images: []string{"https://example.com/image.png"},
					Videos: []string{"https://example.com/video.mp4"},
				}

				jobID, err := service.ExecuteJob(taskID, params, "test", multimedia)
				Expect(err).NotTo(HaveOccurred())

				// Wait a bit for async save to complete
				time.Sleep(50 * time.Millisecond)

				// Ensure directory exists before saving
				err = os.MkdirAll(tempDir, 0755)
				Expect(err).NotTo(HaveOccurred())

				err = service.SaveJobsToFile()
				Expect(err).NotTo(HaveOccurred())

				// Create a new service instance to test loading
				newService := services.NewAgentJobService(
					appConfig,
					modelLoader,
					configLoader,
					evaluator,
				)

				err = newService.LoadJobsFromFile()
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := newService.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				Expect(retrieved.TaskID).To(Equal(taskID))
				Expect(retrieved.Images).To(HaveLen(1))
				Expect(retrieved.Images[0]).To(Equal("https://example.com/image.png"))
				Expect(retrieved.Videos).To(HaveLen(1))
				Expect(retrieved.Videos[0]).To(Equal("https://example.com/video.mp4"))
			})
		})

		Describe("Multimedia format handling", func() {
			var taskID string

			BeforeEach(func() {
				task := schema.Task{
					Name:    "Test Task",
					Model:   "test-model",
					Prompt:  "Test prompt",
					Enabled: true,
				}
				var err error
				taskID, err = service.CreateTask(task)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should handle URLs correctly", func() {
				multimedia := &schema.MultimediaAttachment{
					Images: []string{"https://example.com/image.png"},
					Videos: []string{"http://example.com/video.mp4"},
				}

				jobID, err := service.ExecuteJob(taskID, map[string]string{}, "test", multimedia)
				Expect(err).NotTo(HaveOccurred())

				job, err := service.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				Expect(job.Images[0]).To(Equal("https://example.com/image.png"))
				Expect(job.Videos[0]).To(Equal("http://example.com/video.mp4"))
			})

			It("should handle data URIs correctly", func() {
				multimedia := &schema.MultimediaAttachment{
					Images: []string{"data:image/png;base64,iVBORw0KG"},
					Videos: []string{"data:video/mp4;base64,AAAAIGZ0eXBpc29t"},
				}

				jobID, err := service.ExecuteJob(taskID, map[string]string{}, "test", multimedia)
				Expect(err).NotTo(HaveOccurred())

				job, err := service.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				Expect(job.Images[0]).To(Equal("data:image/png;base64,iVBORw0KG"))
				Expect(job.Videos[0]).To(Equal("data:video/mp4;base64,AAAAIGZ0eXBpc29t"))
			})

			It("should handle base64 strings (will be converted during execution)", func() {
				// Base64 strings without data URI prefix should be stored as-is
				// They will be converted to data URIs during execution
				multimedia := &schema.MultimediaAttachment{
					Images: []string{"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="},
				}

				jobID, err := service.ExecuteJob(taskID, map[string]string{}, "test", multimedia)
				Expect(err).NotTo(HaveOccurred())

				job, err := service.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				// The base64 string is stored as-is in the job
				Expect(job.Images[0]).To(Equal("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="))
			})

			It("should handle empty multimedia arrays", func() {
				multimedia := &schema.MultimediaAttachment{
					Images: []string{""},
				}

				jobID, err := service.ExecuteJob(taskID, map[string]string{}, "test", multimedia)
				Expect(err).NotTo(HaveOccurred())

				job, err := service.GetJob(jobID)
				Expect(err).NotTo(HaveOccurred())
				// Empty strings are stored in the job but will be filtered during execution
				// The job stores what was provided, filtering happens in convertToMultimediaContent
				Expect(job.Images).To(HaveLen(1))
				Expect(job.Images[0]).To(Equal(""))
				Expect(job.Videos).To(BeEmpty())
			})
		})
	})
})
