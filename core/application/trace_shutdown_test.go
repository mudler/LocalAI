// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Backend traces are persisted by a process-wide consumer after the request
// returns. Shutdown must stop those writes into DataPath, or whoever removes
// the directory next (a test's TempDir cleanup, an operator) races them.
var _ = Describe("trace persistence at shutdown", func() {
	traceFiles := func(dir string) []string {
		entries, err := os.ReadDir(filepath.Join(dir, "traces", "backend"))
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	nextID := func() string { return strconv.FormatInt(time.Now().UnixNano(), 10) }
	// Every traced backend call initializes tracing first. The consumer
	// enqueues into the in-memory buffer after it attempted the write, so a
	// buffered trace marks the end of its persistence attempt.
	backendCall := func(app *Application) {
		trace.InitBackendTracingIfEnabled(app.ApplicationConfig().TracingMaxItems, app.ApplicationConfig().TracingMaxBodyBytes)
		id := nextID()
		trace.RecordBackendTrace(trace.BackendTrace{ID: id, Timestamp: time.Now(), Type: trace.BackendTraceLLM})
		Eventually(trace.GetBackendTraces).Should(ContainElement(HaveField("ID", id)))
	}
	start := func(dir string) *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err := New(config.WithContext(ctx), config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableStats(true), config.EnableTracing, config.WithDataPath(dir))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) }) // Shutdown is idempotent
		return app
	}

	It("writes nothing into DataPath after Shutdown, also when a late call initializes tracing again", func() {
		dir := GinkgoT().TempDir()
		app := start(dir)
		backendCall(app)
		before := traceFiles(dir)
		Expect(before).To(HaveLen(1), "a running application persists its backend traces")

		Expect(app.Shutdown()).To(Succeed())
		// A backend call that completes after Shutdown, as an in-flight
		// request does, must not write into the released directory.
		backendCall(app)
		Expect(traceFiles(dir)).To(Equal(before))
	})

	It("persists again when an application starts on the same DataPath in the same process", func() {
		dir := GinkgoT().TempDir()
		first := start(dir)
		backendCall(first)
		Expect(first.Shutdown()).To(Succeed())
		Expect(traceFiles(dir)).To(HaveLen(1))

		second := start(dir)
		backendCall(second)
		Expect(traceFiles(dir)).To(HaveLen(2))
	})
})
