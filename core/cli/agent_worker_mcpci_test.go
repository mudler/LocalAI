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

// recordingWorkConsumer keeps what Consume was asked for, so the spec pins the
// limit the agent worker chooses, not what a carrier does with it.
type recordingWorkConsumer struct {
	kind        messaging.WorkKind
	maxInFlight int
	handler     messaging.WorkHandler
	calls       int
}

func (c *recordingWorkConsumer) Consume(_ context.Context, kind messaging.WorkKind, maxInFlight int, h messaging.WorkHandler) (messaging.Subscription, error) {
	c.calls++
	c.kind, c.maxInFlight, c.handler = kind, maxInFlight, h
	return nil, nil
}

var _ = Describe("startMCPCIConsumer", func() {
	// MCP CI jobs may start stdio servers in containers; running them one at a
	// time per agent worker is the behaviour operators size workers for.
	It("consumes MCP CI jobs one at a time", func() {
		consumer := &recordingWorkConsumer{}
		_, err := startMCPCIConsumer(GinkgoT().Context(), consumer, "http://127.0.0.1:1", "", time.Second)
		Expect(err).ToNot(HaveOccurred())

		Expect(consumer.calls).To(Equal(1))
		Expect(consumer.kind).To(Equal(messaging.WorkMCPCI))
		Expect(consumer.maxInFlight).To(Equal(1))
	})

	It("runs each delivery through handleMCPCIJob on the delivery's events publisher", func() {
		consumer := &recordingWorkConsumer{}
		_, err := startMCPCIConsumer(GinkgoT().Context(), consumer, "http://127.0.0.1:1", "", time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(consumer.handler).ToNot(BeNil())

		events := testutil.NewFakeBus()
		var mu sync.Mutex
		var results []jobs.JobResultEvent
		_, err = events.Subscribe(messaging.SubjectJobResult("job-2"), func(data []byte) {
			var r jobs.JobResultEvent
			Expect(json.Unmarshal(data, &r)).To(Succeed())
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		})
		Expect(err).ToNot(HaveOccurred())

		payload, err := json.Marshal(jobs.JobEvent{
			JobID:  "job-2",
			TaskID: "task-2",
			Job:    &jobs.JobRecord{ID: "job-2"},
			Task:   &jobs.TaskRecord{ID: "task-2", Model: "m"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(consumer.handler(context.Background(), payload, events)).To(Succeed())

		mu.Lock()
		defer mu.Unlock()
		Expect(results).To(ConsistOf(jobs.JobResultEvent{
			JobID:  "job-2",
			Status: "failed",
			Error:  "model config missing from job event",
		}))
	})
})
