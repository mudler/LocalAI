package jobs

import (
	"encoding/json"
	"fmt"
	"sync"

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
		wg.Add(n)
		errs := make([]error, n)

		for i := 0; i < n; i++ {
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				errs[i] = store.AppendJobTrace(job.ID, "step", fmt.Sprintf("step-%d", i))
			}()
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
})
