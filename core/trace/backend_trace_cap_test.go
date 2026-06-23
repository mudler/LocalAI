package trace_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/trace"
)

// The /api/backend-traces endpoint ships up to TracingMaxItems entries to the
// admin Traces UI on every 5s auto-refresh. Without a cap on the per-trace
// Data field, a chatty agent-pool workload (LLM traces carry the full
// `messages` array, TTS traces carry ~1.3 MiB of audio_wav_base64) makes the
// response tens of MiB. The UI then stays in "loading" forever because the
// download + parse runs longer than the refresh interval: the same symptom
// the API-trace fix (commit 61bf34ea) addressed on the other side.
//
// These specs pin the generic safety net (Option A) so any future producer
// that stuffs a large string into Data is automatically bounded.

const (
	smallCap     = 1024
	smallCapStep = 16
)

var _ = Describe("RecordBackendTrace Data capping", func() {
	BeforeEach(func() {
		// The ring buffer is allocated once (sync.Once) but the body cap
		// follows the latest call, so each spec re-establishes smallCap here
		// regardless of what a previous spec set.
		trace.InitBackendTracingIfEnabled(64, smallCap)
		trace.ClearBackendTraces()
	})

	It("replaces oversized top-level string values with a truncation marker", func() {
		oversized := strings.Repeat("x", smallCap*4)

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Type:      trace.BackendTraceLLM,
			ModelName: "m",
			Data: map[string]any{
				"messages": oversized,
				"small":    "fits",
			},
		})

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]

		Expect(got.Data["small"]).To(Equal("fits"), "fields under the cap must pass through untouched")

		// The marker is the contract the UI reads to show truncation; the
		// concrete shape can evolve but it must be a short fixed-size string
		// that encodes the original byte count so users know what was dropped.
		msg, ok := got.Data["messages"].(string)
		Expect(ok).To(BeTrue(), "string fields stay strings after capping")
		Expect(len(msg)).To(BeNumerically("<", smallCap), "capped value must fit under the configured cap")
		Expect(msg).To(ContainSubstring("truncated"))
		Expect(msg).To(ContainSubstring("4096"), "marker should reference the original byte count for diagnostics")
	})

	It("recurses into nested maps so deeply nested oversized strings are also bounded", func() {
		oversized := strings.Repeat("y", smallCap*2)

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Type:      trace.BackendTraceLLM,
			ModelName: "m",
			Data: map[string]any{
				"chat_deltas": map[string]any{
					"content":         oversized,
					"total_deltas":    5,
					"tool_call_count": 0,
				},
			},
		})

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]

		deltas, ok := got.Data["chat_deltas"].(map[string]any)
		Expect(ok).To(BeTrue(), "nested map structure must be preserved")
		Expect(deltas["total_deltas"]).To(Equal(5), "non-string siblings must pass through untouched")

		content, ok := deltas["content"].(string)
		Expect(ok).To(BeTrue())
		Expect(len(content)).To(BeNumerically("<", smallCap), "nested oversized string must still be capped")
		Expect(content).To(ContainSubstring("truncated"))
	})

	It("leaves values within the cap untouched", func() {
		smallVal := strings.Repeat("z", smallCap-smallCapStep)

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Type:      trace.BackendTraceEmbedding,
			ModelName: "m",
			Data: map[string]any{
				"input_text": smallVal,
			},
		})

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]

		Expect(got.Data["input_text"]).To(Equal(smallVal))
	})

	It("does not re-truncate values that producers already capped with TruncateToBytes", func() {
		// Producers (LLM messages/response, etc.) prefer head-preserving
		// truncation so users can still read the start of the conversation.
		// TruncateToBytes guarantees output <= cap, so the generic safety
		// net below must leave it alone, otherwise the kept prefix gets
		// thrown away and replaced with the marker.
		preTruncated := trace.TruncateToBytes(strings.Repeat("a", smallCap*4), smallCap)
		Expect(len(preTruncated)).To(BeNumerically("<=", smallCap))

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Type:      trace.BackendTraceLLM,
			ModelName: "m",
			Data: map[string]any{
				"messages": preTruncated,
			},
		})

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]
		Expect(got.Data["messages"]).To(Equal(preTruncated))
	})

	It("applies a runtime-raised cap without a restart", func() {
		// tracing_max_body_bytes is runtime-mutable via the settings API.
		// Producers like AudioSnippet read the live value, so the recorder
		// must too — under the old first-call-wins behaviour a raised cap
		// kept truncating audio_wav_base64 payloads the producer had already
		// let through, corrupting them into "<truncated: N bytes>" markers.
		oversizedForOldCap := strings.Repeat("w", smallCap*4)

		trace.InitBackendTracingIfEnabled(64, smallCap*8) // simulate the settings raise
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Type:      trace.BackendTraceTranscription,
			ModelName: "m",
			Data: map[string]any{
				"audio_wav_base64": oversizedForOldCap,
			},
		})

		Eventually(trace.GetBackendTraces).Should(HaveLen(1))
		got := trace.GetBackendTraces()[0]
		Expect(got.Data["audio_wav_base64"]).To(Equal(oversizedForOldCap),
			"a payload under the raised cap must survive intact")
	})
})

var _ = Describe("TruncateToBytes", func() {
	It("returns the input unchanged when it fits", func() {
		Expect(trace.TruncateToBytes("hello", 1024)).To(Equal("hello"))
	})

	It("treats maxBytes <= 0 as unlimited", func() {
		Expect(trace.TruncateToBytes("hello", 0)).To(Equal("hello"))
		Expect(trace.TruncateToBytes("hello", -1)).To(Equal("hello"))
	})

	It("caps oversized input to at most maxBytes and preserves the head", func() {
		in := strings.Repeat("a", 5000)
		out := trace.TruncateToBytes(in, 100)
		Expect(len(out)).To(BeNumerically("<=", 100), "output must never exceed the cap so the generic Record-time safety net doesn't fire")
		Expect(out).To(HavePrefix("a"), "should keep the leading content readable")
		Expect(out).To(ContainSubstring("truncated"), "should mark the value as truncated for the UI")
	})

	It("falls back to plain truncation when the cap is smaller than the suffix", func() {
		in := strings.Repeat("a", 100)
		out := trace.TruncateToBytes(in, 4)
		Expect(len(out)).To(Equal(4))
		Expect(out).To(Equal("aaaa"))
	})
})
