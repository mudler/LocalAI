package cli

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("handleMCPCIJob", func() {
	// The handler must report on the publisher the carrier hands it: a
	// carrier without a process-wide bus has no other place to send the
	// terminal result.
	It("publishes a failed result on the events publisher when the model config is missing", func() {
		events := testutil.NewFakeBus()

		var mu sync.Mutex
		var results []jobs.JobResultEvent
		_, err := events.Subscribe(messaging.SubjectJobResult("job-1"), func(data []byte) {
			var r jobs.JobResultEvent
			Expect(json.Unmarshal(data, &r)).To(Succeed())
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		})
		Expect(err).ToNot(HaveOccurred())

		payload, err := json.Marshal(jobs.JobEvent{
			JobID:  "job-1",
			TaskID: "task-1",
			Job:    &jobs.JobRecord{ID: "job-1"},
			Task:   &jobs.TaskRecord{ID: "task-1", Model: "m"},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(handleMCPCIJob(context.Background(), payload, "http://127.0.0.1:1", "", events, time.Second)).To(Succeed())

		mu.Lock()
		defer mu.Unlock()
		Expect(results).To(ConsistOf(jobs.JobResultEvent{
			JobID:  "job-1",
			Status: "failed",
			Error:  "model config missing from job event",
		}))
	})

	It("drops an undecodable payload without publishing anything", func() {
		events := testutil.NewFakeBus()
		var mu sync.Mutex
		published := 0
		// The subject helpers sanitise "*", so the wildcards are spelled out.
		for _, subject := range []string{"jobs.*.result", "jobs.*.progress"} {
			_, err := events.Subscribe(subject, func([]byte) {
				mu.Lock()
				published++
				mu.Unlock()
			})
			Expect(err).ToNot(HaveOccurred())
		}

		Expect(handleMCPCIJob(context.Background(), []byte("not json"), "http://127.0.0.1:1", "", events, time.Second)).To(Succeed())

		mu.Lock()
		defer mu.Unlock()
		Expect(published).To(BeZero())
	})
})
