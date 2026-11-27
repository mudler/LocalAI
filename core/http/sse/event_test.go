package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type sampleDelta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type sampleEvent struct {
	Type  string       `json:"type"`
	Index *int         `json:"index,omitempty"`
	Delta *sampleDelta `json:"delta,omitempty"`
	Score float64      `json:"score,omitempty"`
}

var _ = Describe("WriteEvent", func() {
	It("writes the bytes of fmt.Fprintf and json.Marshal", func() {
		zero := 0
		for i, text := range []string{"", " word", "<b>&amp;</b>", "日本語 🎉", "a\nb\t\"c\"", " ", strings64k()} {
			ev := sampleEvent{Type: "content_block_delta", Index: &zero, Delta: &sampleDelta{Type: "text_delta", Text: text}}
			data, err := json.Marshal(ev)
			Expect(err).ToNot(HaveOccurred())
			want := fmt.Sprintf("event: %s\ndata: %s\n\n", ev.Type, string(data))
			for range 2 { // the second write reuses a pooled buffer
				var got bytes.Buffer
				Expect(WriteEvent(&got, ev.Type, ev)).To(Succeed())
				Expect(got.String()).To(Equal(want), fmt.Sprintf("sample %d", i))
			}
		}
	})

	It("writes nothing for a value it cannot encode", func() {
		var got bytes.Buffer
		err := WriteEvent(&got, "x", sampleEvent{Type: "x", Score: math.NaN()})
		Expect(errors.Is(err, ErrEncode)).To(BeTrue())
		Expect(got.Len()).To(BeZero())
	})
})

func strings64k() string { return string(bytes.Repeat([]byte("x"), 70<<10)) }

// BenchmarkWriteEvent writes one Anthropic-shaped text delta as the streaming
// endpoints did (json.Marshal and fmt.Fprintf of a string copy) and with
// WriteEvent.
func BenchmarkWriteEvent(b *testing.B) {
	zero := 0
	ev := sampleEvent{Type: "content_block_delta", Index: &zero, Delta: &sampleDelta{Type: "text_delta", Text: " word"}}
	b.Run("marshal-fprintf", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			data, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(io.Discard, "event: %s\ndata: %s\n\n", ev.Type, string(data))
		}
	})
	b.Run("write-event", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = WriteEvent(io.Discard, ev.Type, ev)
		}
	})
}
