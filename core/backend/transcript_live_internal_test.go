package backend

import (
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("liveEventFromProto", func() {
	It("maps deltas, eou flags and words (ns -> duration)", func() {
		ev := liveEventFromProto(&proto.TranscriptLiveResponse{
			Delta: "hello ",
			Eou:   true,
			Words: []*proto.TranscriptWord{
				{Start: int64(100 * time.Millisecond), End: int64(400 * time.Millisecond), Text: "hello"},
			},
		})
		Expect(ev.Delta).To(Equal("hello "))
		Expect(ev.Eou).To(BeTrue())
		Expect(ev.Words).To(HaveLen(1))
		Expect(ev.Words[0].Text).To(Equal("hello"))
		Expect(ev.Words[0].Start).To(Equal(100 * time.Millisecond))
		Expect(ev.Words[0].End).To(Equal(400 * time.Millisecond))
		Expect(ev.Final).To(BeNil())
	})

	It("maps the terminal final result including the eou flag", func() {
		ev := liveEventFromProto(&proto.TranscriptLiveResponse{
			FinalResult: &proto.TranscriptResult{
				Text:     "hello world",
				Duration: 1.5,
				Eou:      true,
				Segments: []*proto.TranscriptSegment{{Id: 0, Text: "hello world"}},
			},
		})
		Expect(ev.Final).NotTo(BeNil())
		Expect(ev.Final.Text).To(Equal("hello world"))
		Expect(ev.Final.Duration).To(BeNumerically("~", 1.5, 1e-6))
		Expect(ev.Final.Eou).To(BeTrue())
		Expect(ev.Final.Segments).To(HaveLen(1))
	})

	It("yields an empty event for a bare ready ack (filtered by the recv loop)", func() {
		ev := liveEventFromProto(&proto.TranscriptLiveResponse{Ready: true})
		Expect(ev.Delta).To(BeEmpty())
		Expect(ev.Eou).To(BeFalse())
		Expect(ev.Words).To(BeEmpty())
		Expect(ev.Final).To(BeNil())
	})

	It("maps the eob backchannel flag separately from eou", func() {
		ev := liveEventFromProto(&proto.TranscriptLiveResponse{Delta: "uh-huh", Eob: true})
		Expect(ev.Eob).To(BeTrue())
		Expect(ev.Eou).To(BeFalse())
	})
})

// liveTraceState is what makes streaming-only pipelines visible on the
// Traces page: without it a semantic_vad session with retranscribe off
// produced no transcription trace at all. One trace per session (= one per
// realtime turn), recorded at Close.
var _ = Describe("liveTraceState", func() {
	var appConfig *config.ApplicationConfig

	BeforeEach(func() {
		appConfig = &config.ApplicationConfig{
			EnableTracing:   true,
			TracingMaxItems: 64,
		}
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		trace.ClearBackendTraces()
	})

	modelCfg := func() config.ModelConfig {
		cfg := config.ModelConfig{Backend: "parakeet-cpp"}
		cfg.Name = "parakeet-live"
		return cfg
	}

	It("is disabled (nil) when tracing is off, and nil receivers are no-ops", func() {
		appConfig.EnableTracing = false
		ts := newLiveTraceState(modelCfg(), appConfig, "en")
		Expect(ts).To(BeNil())

		// The session calls these unconditionally; nil must be safe.
		ts.addPCM([]float32{0.5})
		ts.observe(LiveTranscriptionEvent{Eou: true})
		ts.record(nil)
		Consistently(trace.GetBackendTraces, "100ms", "20ms").Should(BeEmpty())
	})

	It("records one transcription trace with text, eou event counts and audio snippet at Close", func() {
		ts := newLiveTraceState(modelCfg(), appConfig, "en")
		Expect(ts).NotTo(BeNil())

		// One second of a loud-ish constant tone so the snippet has signal.
		pcm := make([]float32, liveSampleRate)
		for i := range pcm {
			pcm[i] = 0.25
		}
		ts.addPCM(pcm)
		ts.observe(LiveTranscriptionEvent{Delta: "hello "})
		ts.observe(LiveTranscriptionEvent{Delta: "world", Eou: true})
		ts.observe(LiveTranscriptionEvent{Final: &schema.TranscriptionResult{Text: "hello world", Eou: true}})

		ts.record(nil)

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]
		Expect(got.Type).To(Equal(trace.BackendTraceTranscription))
		Expect(got.ModelName).To(Equal("parakeet-live"))
		Expect(got.Backend).To(Equal("parakeet-cpp"))
		Expect(got.Summary).To(ContainSubstring("hello world"))
		Expect(got.Data["source"]).To(Equal("live_stream"))
		Expect(got.Data["result_text"]).To(Equal("hello world"))
		// The live FinalResult no longer carries a terminal eou flag; the
		// per-feed eou_events count is what the trace records instead.
		Expect(got.Data).NotTo(HaveKey("eou"))
		Expect(got.Data["eou_events"]).To(Equal(1))
		Expect(got.Data["delta_events"]).To(Equal(2))
		Expect(got.Data["audio_duration_s"]).To(BeNumerically("~", 1.0, 0.01))
		Expect(got.Data["audio_wav_base64"]).NotTo(BeEmpty())
		Expect(got.Error).To(BeEmpty())
	})

	It("caps the stored snippet but keeps counting the full fed duration", func() {
		ts := newLiveTraceState(modelCfg(), appConfig, "")

		// Feed past the snippet cap in two chunks (cap + one extra second).
		ts.addPCM(make([]float32, trace.MaxSnippetSeconds*liveSampleRate))
		ts.addPCM(make([]float32, liveSampleRate))

		Expect(len(ts.pcm)).To(Equal(trace.MaxSnippetSeconds * liveSampleRate * 2))
		Expect(ts.fedSamples).To(Equal((trace.MaxSnippetSeconds + 1) * liveSampleRate))

		ts.record(nil)
		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]
		Expect(got.Data["audio_duration_s"]).To(BeNumerically("~", float64(trace.MaxSnippetSeconds+1), 0.01))
		Expect(got.Data["audio_snippet_s"]).To(BeNumerically("~", float64(trace.MaxSnippetSeconds), 0.01))
	})

	It("clamps out-of-range float samples instead of wrapping", func() {
		ts := newLiveTraceState(modelCfg(), appConfig, "")
		ts.addPCM([]float32{2.0, -2.0})
		Expect(ts.pcm).To(Equal([]byte{0xff, 0x7f, 0x00, 0x80})) // 32767, -32768
	})

	It("stamps the close error on the trace", func() {
		ts := newLiveTraceState(modelCfg(), appConfig, "")
		ts.record(errors.New("stream torn down"))

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		Expect(trace.GetBackendTraces()[0].Error).To(Equal("stream torn down"))
	})
})
