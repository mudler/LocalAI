package backend_test

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/model"
)

// ModelLoadTraceObserver is what makes successful loads visible on the
// Traces page: one model_load row per real backend load, carrying the
// resolved backend runtime. Failures must NOT be recorded here — the
// modality wrappers own those — and the observer must respect the runtime
// tracing toggle.
var _ = Describe("ModelLoadTraceObserver", func() {
	var appConfig *config.ApplicationConfig

	successEvent := model.BackendLoadEvent{
		ModelID:    "parakeet-cpp-realtime_eou_120m-v1",
		ModelName:  "realtime_eou_120m.gguf",
		Backend:    "parakeet-cpp",
		BackendURI: "/backends/intel-sycl-f16-parakeet-cpp-development/run.sh",
		Duration:   1500 * time.Millisecond,
	}

	BeforeEach(func() {
		appConfig = &config.ApplicationConfig{
			EnableTracing:   true,
			TracingMaxItems: 64,
		}
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		trace.ClearBackendTraces()
	})

	It("records a model_load trace with the backend runtime on success", func() {
		backend.ModelLoadTraceObserver(appConfig)(successEvent)

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]
		Expect(got.Type).To(Equal(trace.BackendTraceModelLoad))
		Expect(got.Summary).To(Equal("Model loaded"))
		Expect(got.ModelName).To(Equal("parakeet-cpp-realtime_eou_120m-v1"))
		Expect(got.Backend).To(Equal("parakeet-cpp"))
		Expect(got.Duration).To(Equal(1500 * time.Millisecond))
		Expect(got.Data["backend_runtime"]).To(Equal("/backends/intel-sycl-f16-parakeet-cpp-development/run.sh"))
		Expect(got.Data["model_file"]).To(Equal("realtime_eou_120m.gguf"))
		Expect(got.Error).To(BeEmpty())
	})

	It("skips failed loads — the modality wrappers trace those with request context", func() {
		failed := successEvent
		failed.Err = errors.New("grpc service not ready")

		backend.ModelLoadTraceObserver(appConfig)(failed)

		Consistently(trace.GetBackendTraces, "100ms", "20ms").Should(BeEmpty())
	})

	It("records nothing when tracing is disabled", func() {
		appConfig.EnableTracing = false

		backend.ModelLoadTraceObserver(appConfig)(successEvent)

		Consistently(trace.GetBackendTraces, "100ms", "20ms").Should(BeEmpty())
	})
})
