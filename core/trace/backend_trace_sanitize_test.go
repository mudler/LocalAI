package trace_test

import (
	"encoding/json"
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/trace"
)

// encoding/json cannot marshal ±Inf or NaN. The /api/backend-traces endpoint
// serializes the whole buffer with one json call, so a single non-finite float
// in any trace's Data map (e.g. a -Inf dBFS audio metric from a silent clip)
// would fail the entire response and blank the Traces UI. RecordBackendTrace
// must scrub those values regardless of whether a body cap is configured.
var _ = Describe("RecordBackendTrace non-finite float sanitization", func() {
	BeforeEach(func() {
		// maxBodyBytes 0 == no body cap: float sanitization must still run.
		trace.InitBackendTracingIfEnabled(64, 0)
		trace.ClearBackendTraces()
	})

	It("replaces ±Inf and NaN with nil so the response stays JSON-marshalable", func() {
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Type:      trace.BackendTraceTranscription,
			ModelName: "m",
			Data: map[string]any{
				"audio_rms_dbfs":   math.Inf(-1),
				"audio_peak_dbfs":  math.Inf(1),
				"weird":            math.NaN(),
				"audio_duration_s": 1.5, // finite siblings must survive
			},
		})

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]

		Expect(got.Data["audio_rms_dbfs"]).To(BeNil())
		Expect(got.Data["audio_peak_dbfs"]).To(BeNil())
		Expect(got.Data["weird"]).To(BeNil())
		Expect(got.Data["audio_duration_s"]).To(Equal(1.5), "finite floats must pass through untouched")

		_, err := json.Marshal(trace.GetBackendTraces())
		Expect(err).ToNot(HaveOccurred(), "the whole trace buffer must marshal even with non-finite inputs")
	})

	It("scrubs non-finite floats nested in maps and slices", func() {
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Type:      trace.BackendTraceLLM,
			ModelName: "m",
			Data: map[string]any{
				"nested": map[string]any{
					"logprob": math.Inf(-1),
					"ok":      0.25,
				},
				"scores": []any{1.0, math.Inf(1), math.NaN()},
			},
		})

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]

		nested := got.Data["nested"].(map[string]any)
		Expect(nested["logprob"]).To(BeNil())
		Expect(nested["ok"]).To(Equal(0.25))

		scores := got.Data["scores"].([]any)
		Expect(scores[0]).To(Equal(1.0))
		Expect(scores[1]).To(BeNil())
		Expect(scores[2]).To(BeNil())

		_, err := json.Marshal(trace.GetBackendTraces())
		Expect(err).ToNot(HaveOccurred())
	})
})
