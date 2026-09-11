// SPDX-License-Identifier: MIT

package trace_test

import (
	"time"

	"github.com/mudler/LocalAI/core/trace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("live backend traces", func() {
	BeforeEach(func() {
		trace.InitBackendTracingIfEnabled(64, 1024)
		trace.ConfigureBackendTraceMaxInFlight(64)
		trace.ClearBackendTraces()
	})

	It("lists an operation while it is running and replaces it on completion", func() {
		started := time.Now().Add(-time.Millisecond)
		id := trace.BeginBackendTrace(trace.BackendTrace{
			Timestamp: started,
			Type:      trace.BackendTraceLLM,
			ModelName: "slow-model",
			Backend:   "test-backend",
			Summary:   "hello",
		})
		Expect(id).NotTo(BeEmpty())

		running, ok := trace.GetBackendTrace(id)
		Expect(ok).To(BeTrue())
		Expect(running.Status).To(Equal(trace.BackendTraceRunning))
		Expect(running.Duration).To(BeNumerically(">", 0))

		trace.RecordBackendTrace(trace.BackendTrace{
			ID:        id,
			Timestamp: started,
			Duration:  time.Since(started),
			Type:      trace.BackendTraceLLM,
			ModelName: "slow-model",
			Backend:   "test-backend",
			Summary:   "hello -> world",
		})

		Eventually(func() trace.BackendTrace {
			completed, _ := trace.GetBackendTrace(id)
			return completed
		}).Should(HaveField("Status", trace.BackendTraceCompleted))
		Expect(trace.GetBackendTraces()).To(HaveLen(1))
	})

	It("bounds running traces without dropping their eventual completion", func() {
		trace.ConfigureBackendTraceMaxInFlight(1)
		first := trace.BeginBackendTrace(trace.BackendTrace{Timestamp: time.Now(), Type: trace.BackendTraceLLM})
		second := trace.BeginBackendTrace(trace.BackendTrace{Timestamp: time.Now(), Type: trace.BackendTraceEmbedding})
		Expect(first).NotTo(BeEmpty())
		Expect(second).To(BeEmpty())
		Expect(trace.GetBackendTraces()).To(HaveLen(1))

		trace.RecordBackendTrace(trace.BackendTrace{Timestamp: time.Now(), Type: trace.BackendTraceEmbedding})
		trace.RecordBackendTrace(trace.BackendTrace{ID: first, Timestamp: time.Now(), Type: trace.BackendTraceLLM})
		Eventually(trace.GetBackendTraces).Should(HaveLen(2))
	})
})
