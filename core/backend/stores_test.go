package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StoreBackend distributed routing", func() {
	It("carries the persisted model config revision to the router", func() {
		dir := GinkgoT().TempDir()
		configPath := filepath.Join(dir, "conformance.yaml")
		Expect(os.WriteFile(configPath, []byte("name: conformance\nbackend: mock-backend\noptions:\n  - fixture:true\n"), 0o600)).To(Succeed())
		configLoader := config.NewModelConfigLoader(dir)
		Expect(configLoader.LoadModelConfigsFromPath(dir)).To(Succeed())
		cfg, ok := configLoader.GetModelConfig("conformance")
		Expect(ok).To(BeTrue())
		Expect(cfg.PersistedConfigRevision()).ToNot(BeEmpty())

		loader := model.NewModelLoader(&system.SystemState{})
		routerErr := errors.New("stop after capturing store load")
		var routedRevision string
		var routedOptions *pb.ModelOptions
		loader.SetModelRouter(func(_ context.Context, backendName, modelID, modelName, modelFile, revision string, opts *pb.ModelOptions, _ bool) (*model.Model, error) {
			Expect(backendName).To(Equal("mock-backend"))
			Expect(modelID).To(Equal("conformance"))
			Expect(modelName).To(Equal(store.NamespacePrefix + "conformance"))
			Expect(modelFile).To(Equal("store:/conformance"))
			routedRevision = revision
			routedOptions = opts
			return nil, routerErr
		})

		_, err := StoreBackend(loader, config.NewApplicationConfig(), configLoader, "conformance", "")
		Expect(err).To(MatchError(ContainSubstring(routerErr.Error())))
		Expect(routedRevision).To(Equal(cfg.PersistedConfigRevision()))
		Expect(routedOptions.GetOptions()).To(Equal([]string{"fixture:true"}))
	})
})

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
