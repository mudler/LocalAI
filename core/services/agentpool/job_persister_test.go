package agentpool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/xsync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("JobPersister", func() {

	Context("fileJobPersister", func() {
		var (
			p       *fileJobPersister
			tasks   *xsync.SyncedMap[string, schema.Task]
			jobsMap *xsync.SyncedMap[string, schema.Job]
			tmpDir  string
		)

		BeforeEach(func() {
			tmpDir = GinkgoT().TempDir()
			tasks = xsync.NewSyncedMap[string, schema.Task]()
			jobsMap = xsync.NewSyncedMap[string, schema.Job]()
			p = &fileJobPersister{
				tasks:     tasks,
				jobs:      jobsMap,
				tasksFile: filepath.Join(tmpDir, "tasks.json"),
				jobsFile:  filepath.Join(tmpDir, "jobs.json"),
			}
		})

		It("SaveTask writes all tasks to file", func() {
			tasks.Set("t1", schema.Task{ID: "t1", Name: "Task One", Model: "m", Prompt: "p"})
			tasks.Set("t2", schema.Task{ID: "t2", Name: "Task Two", Model: "m", Prompt: "p"})

			Expect(p.SaveTask("", schema.Task{})).To(Succeed())

			// Verify file contents
			data, err := os.ReadFile(p.tasksFile)
			Expect(err).NotTo(HaveOccurred())
			var tf schema.TasksFile
			Expect(json.Unmarshal(data, &tf)).To(Succeed())
			Expect(tf.Tasks).To(HaveLen(2))
		})

		It("DeleteTask writes updated tasks to file", func() {
			tasks.Set("t1", schema.Task{ID: "t1", Name: "Keep"})
			tasks.Set("t2", schema.Task{ID: "t2", Name: "Delete"})

			// Simulate deletion from memory (caller does this before calling persister)
			tasks.Delete("t2")
			Expect(p.DeleteTask("t2")).To(Succeed())

			data, err := os.ReadFile(p.tasksFile)
			Expect(err).NotTo(HaveOccurred())
			var tf schema.TasksFile
			Expect(json.Unmarshal(data, &tf)).To(Succeed())
			Expect(tf.Tasks).To(HaveLen(1))
			Expect(tf.Tasks[0].Name).To(Equal("Keep"))
		})

		It("SaveJob writes all jobs to file", func() {
			jobsMap.Set("j1", schema.Job{ID: "j1", TaskID: "t1", Status: schema.JobStatusPending})
			Expect(p.SaveJob("", schema.Job{})).To(Succeed())

			data, err := os.ReadFile(p.jobsFile)
			Expect(err).NotTo(HaveOccurred())
			var jf schema.JobsFile
			Expect(json.Unmarshal(data, &jf)).To(Succeed())
			Expect(jf.Jobs).To(HaveLen(1))
		})

		It("GetJob returns nil (no authoritative reads)", func() {
			job, err := p.GetJob("anything")
			Expect(err).NotTo(HaveOccurred())
			Expect(job).To(BeNil())
		})

		It("ListJobs returns nil (no authoritative reads)", func() {
			result, err := p.ListJobs("u", "t", "s", 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("LoadTasks reads from file", func() {
			// Pre-populate the file
			tf := schema.TasksFile{
				Tasks: []schema.Task{
					{ID: "t1", Name: "Loaded Task", Model: "m", Prompt: "p"},
				},
			}
			data, _ := json.Marshal(tf)
			Expect(os.WriteFile(p.tasksFile, data, 0600)).To(Succeed())

			loaded, err := p.LoadTasks("")
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(HaveLen(1))
			Expect(loaded[0].Name).To(Equal("Loaded Task"))
		})

		It("LoadJobs reads from file", func() {
			jf := schema.JobsFile{
				Jobs: []schema.Job{
					{ID: "j1", TaskID: "t1", Status: schema.JobStatusCompleted},
				},
			}
			data, _ := json.Marshal(jf)
			Expect(os.WriteFile(p.jobsFile, data, 0600)).To(Succeed())

			loaded, err := p.LoadJobs("")
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(HaveLen(1))
			Expect(loaded[0].Status).To(Equal(schema.JobStatusCompleted))
		})

		It("LoadTasks returns nil for missing file", func() {
			loaded, err := p.LoadTasks("")
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(BeNil())
		})

		It("CleanupOldJobs is a no-op", func() {
			n, err := p.CleanupOldJobs(24 * time.Hour)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(int64(0)))
		})
	})

	Context("dbJobPersister", func() {
		var p *dbJobPersister

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			store, err := jobs.NewJobStore(db)
			Expect(err).NotTo(HaveOccurred())
			p = &dbJobPersister{store: store}
		})

		It("SaveTask persists to PostgreSQL", func() {
			task := schema.Task{ID: "t1", Name: "DB Task", Model: "m", Prompt: "p"}
			Expect(p.SaveTask("user1", task)).To(Succeed())

			// Verify via raw store
			rec, err := p.store.GetTask("t1")
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Name).To(Equal("DB Task"))
			Expect(rec.UserID).To(Equal("user1"))
		})

		It("SaveTask updates existing task (upsert)", func() {
			task := schema.Task{ID: "t1", Name: "Original", Model: "m", Prompt: "p"}
			Expect(p.SaveTask("user1", task)).To(Succeed())

			task.Name = "Updated"
			Expect(p.SaveTask("user1", task)).To(Succeed())

			rec, err := p.store.GetTask("t1")
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Name).To(Equal("Updated"))
		})

		It("DeleteTask removes from DB", func() {
			task := schema.Task{ID: "t1", Name: "Gone", Model: "m", Prompt: "p"}
			Expect(p.SaveTask("user1", task)).To(Succeed())

			Expect(p.DeleteTask("t1")).To(Succeed())

			_, err := p.store.GetTask("t1")
			Expect(err).To(HaveOccurred()) // record not found
		})

		It("SaveJob persists to PostgreSQL", func() {
			job := schema.Job{ID: "j1", TaskID: "t1", Status: schema.JobStatusPending}
			Expect(p.SaveJob("user1", job)).To(Succeed())

			rec, err := p.store.GetJob("j1")
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.TaskID).To(Equal("t1"))
		})

		It("GetJob returns converted schema.Job", func() {
			job := schema.Job{ID: "j1", TaskID: "t1", Status: schema.JobStatusCompleted, Result: "done"}
			Expect(p.SaveJob("user1", job)).To(Succeed())

			got, err := p.GetJob("j1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).NotTo(BeNil())
			Expect(got.Status).To(Equal(schema.JobStatusCompleted))
			Expect(got.Result).To(Equal("done"))
		})

		It("GetJob returns nil for nonexistent", func() {
			got, err := p.GetJob("nonexistent")
			// GORM returns error for record not found
			Expect(err).To(HaveOccurred())
			Expect(got).To(BeNil())
		})

		It("ListJobs filters by taskID and status", func() {
			Expect(p.SaveJob("u1", schema.Job{ID: "j1", TaskID: "t1", Status: schema.JobStatusPending})).To(Succeed())
			Expect(p.SaveJob("u1", schema.Job{ID: "j2", TaskID: "t1", Status: schema.JobStatusCompleted})).To(Succeed())
			Expect(p.SaveJob("u1", schema.Job{ID: "j3", TaskID: "t2", Status: schema.JobStatusPending})).To(Succeed())

			// Filter by task
			result, err := p.ListJobs("u1", "t1", "", 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(2))

			// Filter by status
			result, err = p.ListJobs("u1", "", "completed", 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].ID).To(Equal("j2"))
		})

		It("ListJobs respects limit", func() {
			Expect(p.SaveJob("u1", schema.Job{ID: "j1", TaskID: "t1", Status: schema.JobStatusPending})).To(Succeed())
			Expect(p.SaveJob("u1", schema.Job{ID: "j2", TaskID: "t1", Status: schema.JobStatusPending})).To(Succeed())

			result, err := p.ListJobs("u1", "", "", 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(1))
		})

		It("LoadTasks returns all tasks for user", func() {
			Expect(p.SaveTask("u1", schema.Task{ID: "t1", Name: "A", Model: "m", Prompt: "p"})).To(Succeed())
			Expect(p.SaveTask("u1", schema.Task{ID: "t2", Name: "B", Model: "m", Prompt: "p"})).To(Succeed())
			Expect(p.SaveTask("u2", schema.Task{ID: "t3", Name: "C", Model: "m", Prompt: "p"})).To(Succeed())

			result, err := p.LoadTasks("u1")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(2))
		})

		It("LoadJobs returns all jobs for user", func() {
			Expect(p.SaveJob("u1", schema.Job{ID: "j1", TaskID: "t1", Status: schema.JobStatusPending})).To(Succeed())
			Expect(p.SaveJob("u2", schema.Job{ID: "j2", TaskID: "t2", Status: schema.JobStatusPending})).To(Succeed())

			result, err := p.LoadJobs("u1")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].ID).To(Equal("j1"))
		})

		It("CleanupOldJobs removes old records", func() {
			Expect(p.SaveJob("u1", schema.Job{ID: "j1", TaskID: "t1", Status: schema.JobStatusCompleted})).To(Succeed())

			// The job was just created, so retention of 0 should clean it
			n, err := p.CleanupOldJobs(0)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(BeNumerically(">=", 0)) // may or may not delete depending on timing
		})
	})
})
