package openai

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sseChunkSamples() []schema.OpenAIResponse {
	texts := []string{"", " word", "<b>&amp;</b>", "日本語 🎉", "line\nbreak\t\"quoted\"", "  ", "\x7f"}
	var out []schema.OpenAIResponse
	for i, t := range texts {
		content := t
		reasoning := t + " why"
		out = append(out,
			schema.OpenAIResponse{ID: "id", Created: i, Model: "m", Object: "chat.completion.chunk",
				Choices: []schema.Choice{{Index: 0, Delta: &schema.Message{Content: &content}}}},
			schema.OpenAIResponse{ID: "id", Created: i, Model: "m", Object: "chat.completion.chunk",
				Choices: []schema.Choice{{Index: 0, Delta: &schema.Message{Role: "assistant", Reasoning: &reasoning}}}},
			schema.OpenAIResponse{ID: "id", Created: i, Model: "m", Object: "chat.completion.chunk",
				Choices: []schema.Choice{{Index: 0, Delta: &schema.Message{ToolCalls: []schema.ToolCall{{
					Index: 0, ID: "call", Type: "function",
					FunctionCall: schema.FunctionCall{Name: "f", Arguments: `{"q":"` + t + `"}`},
				}}}}}},
		)
	}
	return out
}

var _ = Describe("sseChunkWriter", func() {
	It("writes the bytes of Fprintf and json.Marshal", func() {
		var w sseChunkWriter
		for i, ev := range sseChunkSamples() {
			data, err := json.Marshal(ev)
			Expect(err).ToNot(HaveOccurred())
			want := fmt.Sprintf("data: %s\n\n", string(data))
			got, err := w.encode(ev)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal(want), fmt.Sprintf("sample %d", i))
			Expect(string(w.payload())).To(Equal(string(data)), fmt.Sprintf("sample %d", i))
		}
	})
})

// BenchmarkSSEChunk encodes one chat chunk as the stream loop did
// (json.Marshal, a string copy for the debug line, fmt.Fprintf of another
// copy) and with sseChunkWriter.
func BenchmarkSSEChunk(b *testing.B) {
	content := " word"
	ev := schema.OpenAIResponse{ID: "0d11e42a-18c8-40c4-9607-71aadfd6ee6d", Created: 1795768536, Model: "bench", Object: "chat.completion.chunk",
		Choices: []schema.Choice{{Index: 0, Delta: &schema.Message{Content: &content}}}}
	var sink discardWriter
	b.Run("marshal-fprintf", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			data, _ := json.Marshal(ev)
			_ = string(data)
			_, _ = fmt.Fprintf(&sink, "data: %s\n\n", string(data))
		}
	})
	b.Run("chunk-writer", func(b *testing.B) {
		b.ReportAllocs()
		var w sseChunkWriter
		for b.Loop() {
			data, _ := w.encode(ev)
			_, _ = sink.Write(data)
		}
	})
}

type discardWriter struct{}

func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }
