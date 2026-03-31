package jobs

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("JobStore", func() {
	var store *JobStore

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		store, err = NewJobStore(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(store).ToNot(BeNil())
	})

	It("auto-migrates on creation", func() {
		// If we got here, NewJobStore succeeded — migration worked.
	})

	It("appends a single trace", func() {
		job := &JobRecord{
			TaskID: "task-1",
			UserID: "user-1",
			Status: "running",
		}
		Expect(store.CreateJob(job)).To(Succeed())
		Expect(store.AppendJobTrace(job.ID, "status", "started processing")).To(Succeed())

		got, err := store.GetJob(job.ID)
		Expect(err).ToNot(HaveOccurred())

		var traces []map[string]string
		Expect(json.Unmarshal([]byte(got.TracesJSON), &traces)).To(Succeed())
		Expect(traces).To(HaveLen(1))
		Expect(traces[0]["type"]).To(Equal("status"))
		Expect(traces[0]["content"]).To(Equal("started processing"))
	})

	It("appends to existing traces", func() {
		existingTrace := []map[string]string{{"type": "init", "content": "initialized"}}
		existingJSON, _ := json.Marshal(existingTrace)

		job := &JobRecord{
			TaskID:     "task-2",
			UserID:     "user-1",
			Status:     "running",
			TracesJSON: string(existingJSON),
		}
		Expect(store.CreateJob(job)).To(Succeed())
		Expect(store.AppendJobTrace(job.ID, "action", "tool called")).To(Succeed())

		got, err := store.GetJob(job.ID)
		Expect(err).ToNot(HaveOccurred())

		var traces []map[string]string
		Expect(json.Unmarshal([]byte(got.TracesJSON), &traces)).To(Succeed())
		Expect(traces).To(HaveLen(2))
		Expect(traces[0]["type"]).To(Equal("init"))
		Expect(traces[1]["type"]).To(Equal("action"))
	})

	It("handles concurrent trace appends", func() {
		job := &JobRecord{
			TaskID: "task-concurrent",
			UserID: "user-1",
			Status: "running",
		}
		Expect(store.CreateJob(job)).To(Succeed())

		const n = 10
		var wg sync.WaitGroup
		errs := make([]error, n)

		for i := range n {
			wg.Go(func() {
				defer GinkgoRecover()
				errs[i] = store.AppendJobTrace(job.ID, "step", fmt.Sprintf("step-%d", i))
			})
		}
		wg.Wait()

		for i, e := range errs {
			Expect(e).ToNot(HaveOccurred(), fmt.Sprintf("goroutine %d", i))
		}

		got, err := store.GetJob(job.ID)
		Expect(err).ToNot(HaveOccurred())

		var traces []map[string]string
		Expect(json.Unmarshal([]byte(got.TracesJSON), &traces)).To(Succeed())
		// BUG B1: The read-modify-write in AppendJobTrace is not atomic,
		// so concurrent appends can lose traces. This test documents the bug.
		Expect(traces).To(HaveLen(n), "race condition — bug B1")
	})

	It("creates and gets a task", func() {
		task := &TaskRecord{
			UserID:      "user-42",
			Name:        "my-task",
			Description: "A test task",
			Model:       "gpt-4",
			Prompt:      "Do something",
			Enabled:     true,
		}
		Expect(store.CreateTask(task)).To(Succeed())
		Expect(task.ID).ToNot(BeEmpty())

		got, err := store.GetTask(task.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.UserID).To(Equal("user-42"))
		Expect(got.Name).To(Equal("my-task"))
		Expect(got.Description).To(Equal("A test task"))
		Expect(got.Model).To(Equal("gpt-4"))
		Expect(got.Prompt).To(Equal("Do something"))
		Expect(got.Enabled).To(BeTrue())
	})

	It("creates and gets a job", func() {
		job := &JobRecord{
			TaskID:      "task-99",
			UserID:      "user-99",
			Status:      "pending",
			TriggeredBy: "manual",
			FrontendID:  "frontend-1",
		}
		Expect(store.CreateJob(job)).To(Succeed())
		Expect(job.ID).ToNot(BeEmpty())

		got, err := store.GetJob(job.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.TaskID).To(Equal("task-99"))
		Expect(got.UserID).To(Equal("user-99"))
		Expect(got.Status).To(Equal("pending"))
		Expect(got.TriggeredBy).To(Equal("manual"))
		Expect(got.FrontendID).To(Equal("frontend-1"))
	})

	Describe("UpdateJobStatus", func() {
		var job *JobRecord

		BeforeEach(func() {
			job = &JobRecord{
				TaskID: "task-status",
				UserID: "user-1",
				Status: "pending",
			}
			Expect(store.CreateJob(job)).To(Succeed())
		})

		It("sets started_at when status becomes running", func() {
			Expect(store.UpdateJobStatus(job.ID, "running", "", "")).To(Succeed())

			got, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("running"))
			Expect(got.StartedAt).ToNot(BeNil())
			Expect(got.CompletedAt).To(BeNil())
		})

		It("sets completed_at when status becomes completed", func() {
			Expect(store.UpdateJobStatus(job.ID, "completed", "some result", "")).To(Succeed())

			got, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("completed"))
			Expect(got.CompletedAt).ToNot(BeNil())
			Expect(got.Result).To(Equal("some result"))
		})

		It("sets completed_at and error when status becomes failed", func() {
			Expect(store.UpdateJobStatus(job.ID, "failed", "", "something broke")).To(Succeed())

			got, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("failed"))
			Expect(got.CompletedAt).ToNot(BeNil())
			Expect(got.Error).To(Equal("something broke"))
		})

		It("sets completed_at when status becomes cancelled", func() {
			Expect(store.UpdateJobStatus(job.ID, "cancelled", "", "")).To(Succeed())

			got, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("cancelled"))
			Expect(got.CompletedAt).ToNot(BeNil())
		})
	})

	Describe("CleanupOldJobs", func() {
		It("deletes only jobs older than retention period", func() {
			oldJob := &JobRecord{
				TaskID: "task-old",
				UserID: "user-1",
				Status: "completed",
			}
			Expect(store.CreateJob(oldJob)).To(Succeed())

			// Manually backdate the old job's created_at
			store.db.Model(&JobRecord{}).Where("id = ?", oldJob.ID).
				Update("created_at", time.Now().Add(-48*time.Hour))

			newJob := &JobRecord{
				TaskID: "task-new",
				UserID: "user-1",
				Status: "completed",
			}
			Expect(store.CreateJob(newJob)).To(Succeed())

			deleted, err := store.CleanupOldJobs(24 * time.Hour)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeNumerically("==", 1))

			// Old job should be gone
			_, err = store.GetJob(oldJob.ID)
			Expect(err).To(HaveOccurred())

			// New job should remain
			got, err := store.GetJob(newJob.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.ID).To(Equal(newJob.ID))
		})

		It("returns zero when no old jobs exist", func() {
			job := &JobRecord{
				TaskID: "task-fresh",
				UserID: "user-1",
				Status: "pending",
			}
			Expect(store.CreateJob(job)).To(Succeed())

			deleted, err := store.CleanupOldJobs(24 * time.Hour)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(BeNumerically("==", 0))
		})
	})

	Describe("ListTasks", func() {
		BeforeEach(func() {
			for i := range 3 {
				task := &TaskRecord{
					UserID:  "user-list",
					Name:    fmt.Sprintf("task-%d", i),
					Model:   "gpt-4",
					Prompt:  "test",
					Enabled: true,
				}
				Expect(store.CreateTask(task)).To(Succeed())
			}
			// Create a task for a different user
			Expect(store.CreateTask(&TaskRecord{
				UserID:  "user-other",
				Name:    "other-task",
				Model:   "gpt-4",
				Prompt:  "test",
				Enabled: true,
			})).To(Succeed())
		})

		It("returns all tasks when userID is empty", func() {
			tasks, err := store.ListTasks("")
			Expect(err).ToNot(HaveOccurred())
			Expect(tasks).To(HaveLen(4))
		})

		It("filters tasks by userID", func() {
			tasks, err := store.ListTasks("user-list")
			Expect(err).ToNot(HaveOccurred())
			Expect(tasks).To(HaveLen(3))
			for _, t := range tasks {
				Expect(t.UserID).To(Equal("user-list"))
			}
		})
	})

	Describe("DeleteTask", func() {
		It("removes a task and confirms it is gone", func() {
			task := &TaskRecord{
				UserID:  "user-del",
				Name:    "to-delete",
				Model:   "gpt-4",
				Prompt:  "test",
				Enabled: true,
			}
			Expect(store.CreateTask(task)).To(Succeed())

			_, err := store.GetTask(task.ID)
			Expect(err).ToNot(HaveOccurred())

			Expect(store.DeleteTask(task.ID)).To(Succeed())

			_, err = store.GetTask(task.ID)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("ListJobs with filters", func() {
		BeforeEach(func() {
			jobs := []JobRecord{
				{TaskID: "task-a", UserID: "user-a", Status: "pending", TriggeredBy: "manual"},
				{TaskID: "task-a", UserID: "user-a", Status: "running", TriggeredBy: "manual"},
				{TaskID: "task-a", UserID: "user-a", Status: "completed", TriggeredBy: "cron"},
				{TaskID: "task-b", UserID: "user-b", Status: "pending", TriggeredBy: "manual"},
				{TaskID: "task-b", UserID: "user-b", Status: "failed", TriggeredBy: "manual"},
			}
			for i := range jobs {
				Expect(store.CreateJob(&jobs[i])).To(Succeed())
			}
		})

		It("filters by status", func() {
			jobs, err := store.ListJobs("", "", "pending", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(2))
			for _, j := range jobs {
				Expect(j.Status).To(Equal("pending"))
			}
		})

		It("filters by userID", func() {
			jobs, err := store.ListJobs("user-a", "", "", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(3))
			for _, j := range jobs {
				Expect(j.UserID).To(Equal("user-a"))
			}
		})

		It("filters by taskID", func() {
			jobs, err := store.ListJobs("", "task-b", "", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(2))
			for _, j := range jobs {
				Expect(j.TaskID).To(Equal("task-b"))
			}
		})

		It("combines filters", func() {
			jobs, err := store.ListJobs("user-a", "task-a", "completed", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(1))
			Expect(jobs[0].Status).To(Equal("completed"))
		})

		It("respects limit", func() {
			jobs, err := store.ListJobs("", "", "", 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(2))
		})

		It("returns all jobs when no filters are set", func() {
			jobs, err := store.ListJobs("", "", "", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(5))
		})
	})
})
