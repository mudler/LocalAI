// SPDX-License-Identifier: MIT

package trace_test

import (
	"path/filepath"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/core/trace/tracepersist"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend trace persistence", func() {
	It("restores traces and advances the ID sequence", func() {
		dataPath := GinkgoT().TempDir()
		store, err := tracepersist.New[trace.BackendTrace](filepath.Join(dataPath, "traces", "backend"), 4)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Append("41", trace.BackendTrace{
			ID: "41", Timestamp: time.Now(), Type: trace.BackendTraceLLM,
		})).To(Succeed())

		trace.ConfigureBackendTracePersistence(dataPath)
		trace.InitBackendTracingIfEnabled(4, 0)

		Eventually(trace.GetBackendTraces).Should(ContainElement(HaveField("ID", "41")))
		trace.RecordBackendTrace(trace.BackendTrace{Timestamp: time.Now(), Type: trace.BackendTraceLLM})
		Eventually(trace.GetBackendTraces).Should(HaveLen(2))
		id, err := strconv.ParseUint(trace.GetBackendTraces()[0].ID, 10, 64)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(BeNumerically(">", 41))
	})

	It("serializes clear behind records already queued for persistence", func() {
		dataPath := GinkgoT().TempDir()
		trace.ConfigureBackendTracePersistence(dataPath)
		trace.InitBackendTracingIfEnabled(64, 0)

		for i := range 50 {
			trace.RecordBackendTrace(trace.BackendTrace{
				ID: strconv.Itoa(i + 1), Timestamp: time.Now(), Type: trace.BackendTraceLLM,
			})
		}
		trace.ClearBackendTraces()

		store, err := tracepersist.New[trace.BackendTrace](filepath.Join(dataPath, "traces", "backend"), 64)
		Expect(err).NotTo(HaveOccurred())
		records, err := store.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
		Expect(trace.GetBackendTraces()).To(BeEmpty())
	})
})
