// SPDX-License-Identifier: MIT

package middleware

import (
	"path/filepath"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/trace/tracepersist"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("API trace persistence", func() {
	It("restores before request execution and advances the ID sequence", func() {
		dataPath := GinkgoT().TempDir()
		store, err := tracepersist.New[APIExchange](filepath.Join(dataPath, "traces", "api"), 4)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Append("41", APIExchange{ID: "41", Timestamp: time.Now()})).To(Succeed())

		initializeTracing(dataPath, 4)

		Expect(GetTraces()).To(ContainElement(HaveField("ID", "41")))
		id, err := strconv.ParseUint(nextTraceID(), 10, 64)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(BeNumerically(">", 41))
	})

	It("serializes clear behind records already queued for persistence", func() {
		dataPath := GinkgoT().TempDir()
		initializeTracing(dataPath, 64)
		for i := range 50 {
			exchange := APIExchange{ID: strconv.Itoa(i + 1), Timestamp: time.Now()}
			logChan <- traceCommand{exchange: &exchange, store: traceStore}
		}

		ClearTraces()

		store, err := tracepersist.New[APIExchange](filepath.Join(dataPath, "traces", "api"), 64)
		Expect(err).NotTo(HaveOccurred())
		records, err := store.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
		Expect(GetTraces()).To(BeEmpty())
	})
})
