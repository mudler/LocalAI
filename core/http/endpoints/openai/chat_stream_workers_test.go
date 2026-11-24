package openai

import (
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// drainChannel reads everything currently buffered on a channel without
// blocking on close. The helper test channels are sized for the assertions.
func drainChannel(ch <-chan schema.OpenAIResponse) []schema.OpenAIResponse {
	var out []schema.OpenAIResponse
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, r)
		default:
			return out
		}
	}
}

// nameOf returns the name of the first tool call carried on the choice's
// delta, or "" if none.
func nameOf(r schema.OpenAIResponse) string {
	if len(r.Choices) == 0 || r.Choices[0].Delta == nil {
		return ""
	}
	if len(r.Choices[0].Delta.ToolCalls) == 0 {
		return ""
	}
	return r.Choices[0].Delta.ToolCalls[0].FunctionCall.Name
}

var _ = Describe("emitJSONToolCallDeltas", func() {
	const (
		id      = "test-stream"
		model   = "test-model"
		created = 1700000000
	)

	// The case that motivated this helper. With the previous version of
	// the streaming worker, ParseJSONIterative would hand back a stub
	// object like `{"4310046988783340008":1}` after the model had only
	// emitted `{`. The worker bumped lastEmittedCount unconditionally,
	// which permanently gated off content emission for the rest of the
	// stream (qwen3-4b with stream:true + tools dribbled only `{"` to
	// the client and then nothing). See issue #9988.
	Context("partial stub without a usable name", func() {
		It("does NOT bump lastEmittedCount and emits nothing", func() {
			responses := make(chan schema.OpenAIResponse, 4)
			// What ParseJSONIterative used to return for `{`:
			stubResults := []map[string]any{
				{"4310046988783340008": float64(1)},
			}

			next := emitJSONToolCallDeltas(stubResults, 0, id, model, created, responses)

			Expect(next).To(Equal(0),
				"lastEmittedCount must NOT advance past a stub without a name "+
					"— otherwise content emission gets permanently gated off")
			Expect(drainChannel(responses)).To(BeEmpty(),
				"no tool_call chunk should be emitted for a stub without a name")
		})
	})

	// No-regression #1: the autoparser-correctly-working path. When the
	// C++ autoparser classifies tool calls itself, the raw text result is
	// cleared and ParseJSONIterative on it returns no results — this
	// helper must be a no-op so the deferred end-of-stream code can emit
	// the tool calls from TokenUsage.ChatDeltas.
	Context("empty jsonResults (autoparser-correctly-working path)", func() {
		It("is a no-op and leaves lastEmittedCount unchanged", func() {
			responses := make(chan schema.OpenAIResponse, 4)
			next := emitJSONToolCallDeltas(nil, 0, id, model, created, responses)
			Expect(next).To(Equal(0))
			Expect(drainChannel(responses)).To(BeEmpty())
		})

		It("leaves a non-zero lastEmittedCount unchanged when later called with the same length", func() {
			responses := make(chan schema.OpenAIResponse, 4)
			results := []map[string]any{
				{"name": "search", "arguments": map[string]any{"q": "hi"}},
			}
			// First call emits the one available tool call.
			next := emitJSONToolCallDeltas(results, 0, id, model, created, responses)
			Expect(next).To(Equal(1))
			Expect(drainChannel(responses)).To(HaveLen(1))

			// Subsequent chunks haven't grown the slice — must be a no-op.
			next = emitJSONToolCallDeltas(results, next, id, model, created, responses)
			Expect(next).To(Equal(1))
			Expect(drainChannel(responses)).To(BeEmpty())
		})
	})

	// No-regression #2: the normal completed-JSON path. When the model
	// emits a real, complete tool call as JSON in raw content (e.g. qwen3
	// without jinja but with tools), we should emit exactly one tool_call
	// SSE chunk on the first call and become a no-op on later calls.
	Context("single complete tool call", func() {
		It("emits one tool_call chunk and bumps lastEmittedCount to 1", func() {
			responses := make(chan schema.OpenAIResponse, 4)
			results := []map[string]any{
				{
					"name": "search",
					"arguments": map[string]any{
						"q": "hello",
					},
				},
			}

			next := emitJSONToolCallDeltas(results, 0, id, model, created, responses)

			Expect(next).To(Equal(1))
			out := drainChannel(responses)
			Expect(out).To(HaveLen(1))
			Expect(nameOf(out[0])).To(Equal("search"))
			Expect(out[0].Choices[0].Delta.ToolCalls[0].FunctionCall.Arguments).
				To(ContainSubstring(`"q":"hello"`))
		})

		It("accepts arguments already serialized as a string", func() {
			responses := make(chan schema.OpenAIResponse, 4)
			results := []map[string]any{
				{
					"name":      "search",
					"arguments": `{"q":"hello"}`,
				},
			}

			emitJSONToolCallDeltas(results, 0, id, model, created, responses)

			out := drainChannel(responses)
			Expect(out).To(HaveLen(1))
			Expect(out[0].Choices[0].Delta.ToolCalls[0].FunctionCall.Arguments).
				To(Equal(`{"q":"hello"}`))
		})
	})

	// No-regression #3: multiple tool calls (parallel tool calling).
	// Both must be emitted, lastEmittedCount must end at 2.
	Context("multiple complete tool calls", func() {
		It("emits one chunk per tool call and bumps lastEmittedCount to len(results)", func() {
			responses := make(chan schema.OpenAIResponse, 8)
			results := []map[string]any{
				{"name": "search", "arguments": map[string]any{"q": "a"}},
				{"name": "browse", "arguments": map[string]any{"url": "b"}},
			}

			next := emitJSONToolCallDeltas(results, 0, id, model, created, responses)

			Expect(next).To(Equal(2))
			out := drainChannel(responses)
			Expect(out).To(HaveLen(2))
			Expect(nameOf(out[0])).To(Equal("search"))
			Expect(nameOf(out[1])).To(Equal("browse"))
		})
	})

	// The streaming-tail case: incremental chunks. First parse returns
	// one complete tool call followed by a partial stub; later chunks
	// complete the second tool call. We must emit the first immediately
	// and the second on the later call — without ever bumping past the
	// stub mid-stream.
	Context("partial tail behind a real tool call", func() {
		It("emits the complete entry, stops at the stub, and resumes once the tail completes", func() {
			responses := make(chan schema.OpenAIResponse, 8)

			// Chunk 1: one real call + a partial stub for the next.
			chunk1 := []map[string]any{
				{"name": "search", "arguments": map[string]any{"q": "a"}},
				{"4310046988783340008": float64(1)},
			}
			next := emitJSONToolCallDeltas(chunk1, 0, id, model, created, responses)
			Expect(next).To(Equal(1),
				"must NOT advance to 2 — the stub at index 1 has no usable name")
			out := drainChannel(responses)
			Expect(out).To(HaveLen(1))
			Expect(nameOf(out[0])).To(Equal("search"))

			// Chunk 2: the stub completes into a real call.
			chunk2 := []map[string]any{
				{"name": "search", "arguments": map[string]any{"q": "a"}},
				{"name": "browse", "arguments": map[string]any{"url": "b"}},
			}
			next = emitJSONToolCallDeltas(chunk2, next, id, model, created, responses)
			Expect(next).To(Equal(2))
			out = drainChannel(responses)
			Expect(out).To(HaveLen(1))
			Expect(nameOf(out[0])).To(Equal("browse"))
		})
	})
})
