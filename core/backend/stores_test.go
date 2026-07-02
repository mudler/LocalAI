package backend

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// findVectorStoreTrace returns the most recent vector_store trace whose
// model_name matches storeName, or nil if none was recorded. Used by
// the specs below to assert the trace landed without relying on
// ring-buffer ordering across other tests in the suite.
func findVectorStoreTrace(storeName string) *trace.BackendTrace {
	traces := trace.GetBackendTraces()
	for i := range traces {
		bt := &traces[i]
		if bt.Type == trace.BackendTraceVectorStore && bt.ModelName == storeName {
			return bt
		}
	}
	return nil
}

var _ = Describe("localVectorStore tracing", func() {
	// Pin the trace surface admins read from /api/backend-traces.
	// The original failure mode that motivated these specs — the
	// local-store backend not installed — was silent on every surface
	// except a per-call xlog.Warn. With tracing wired in, the row
	// appears next to the embedder/score traces for the same request.
	BeforeEach(func() {
		trace.ClearBackendTraces()
	})

	It("records a vector_store trace with outcome=backend_load_error when the backend can't be loaded", func() {
		// nil ModelLoader → s.backend → StoreBackend → panics on load.
		// Use a real-but-empty loader so the failure surfaces as an
		// error instead, exercising the load-failure trace path the
		// admin would hit when local-store isn't installed.
		appCfg := &config.ApplicationConfig{
			EnableTracing:       true,
			TracingMaxItems:     16,
			TracingMaxBodyBytes: 1024,
		}
		s := &localVectorStore{
			loader:    model.NewModelLoader(&system.SystemState{}),
			appConfig: appCfg,
			storeName: "router-cache-test",
		}

		// Search must surface the error AND record a trace describing it.
		_, _, _, err := s.Search(context.Background(), []float32{0.1, 0.2, 0.3})
		Expect(err).To(HaveOccurred())

		Eventually(func() *trace.BackendTrace {
			return findVectorStoreTrace("router-cache-test")
		}).ShouldNot(BeNil())

		bt := findVectorStoreTrace("router-cache-test")
		Expect(bt.Backend).To(Equal(model.LocalStoreBackend))
		Expect(bt.Data["op"]).To(Equal("search"))
		Expect(bt.Data["outcome"]).To(Equal("backend_load_error"))
		Expect(bt.Data["vector_dim"]).To(Equal(3))
		// Error is the wrapped "vector store load: …" surfaced to the caller.
		Expect(bt.Error).To(ContainSubstring("vector store load"))
	})

	It("does not record a trace when tracing is disabled", func() {
		// Opt-out path: appConfig.EnableTracing=false must short-circuit
		// before InitBackendTracingIfEnabled, so a workload with tracing
		// turned off doesn't pay the channel-send cost per cache call.
		appCfg := &config.ApplicationConfig{EnableTracing: false}
		s := &localVectorStore{
			loader:    model.NewModelLoader(&system.SystemState{}),
			appConfig: appCfg,
			storeName: "router-cache-disabled",
		}
		_, _, _, _ = s.Search(context.Background(), []float32{1})
		Consistently(func() *trace.BackendTrace {
			return findVectorStoreTrace("router-cache-disabled")
		}).Should(BeNil())
	})
})
