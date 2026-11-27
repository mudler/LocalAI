package middleware

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// discardWriter is a ResponseWriter that drops the body.
type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(int)             {}

func newDiscardWriter(contentType string) *discardWriter {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &discardWriter{h: h}
}

var usageLines = []string{
	`data: {"model":"m","choices":[{"delta":{"content":"hi"}}]}`,
	`data: {"model":"m","usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
	`data: {"model":"a","usage":{"input_tokens":2,"output_tokens":7}}`,
	`data: {"type":"message_delta","usage":{"output_tokens":4}}`,
	`data: [DONE]`, `event: message_stop`, `: ping`, ``, `data:`, `data: `, `data: not json`,
	"data: {\"model\":\"m\",\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\r",
}

type usageResult struct {
	model                     string
	prompt, completion, total int64
	ok                        bool
}

func usageThroughWriter(contentType string, body []byte, r *rand.Rand) usageResult {
	w := &usageBodyWriter{ResponseWriter: newDiscardWriter(contentType)}
	for len(body) > 0 {
		n := min(r.Intn(40), len(body))
		_, _ = w.Write(body[:n])
		body = body[n:]
	}
	var u usageResult
	u.model, u.prompt, u.completion, u.total, u.ok = w.tokens(contentType)
	return u
}

var _ = Describe("usageBodyWriter", func() {
	It("finds the usage tokensFromBody finds in the whole body", func() {
		r := rand.New(rand.NewSource(5))
		for run := range 4000 {
			var body bytes.Buffer
			for range r.Intn(8) {
				body.WriteString(usageLines[r.Intn(len(usageLines))])
				if r.Intn(5) > 0 {
					body.WriteString("\n")
				}
				if r.Intn(3) == 0 {
					body.WriteString("\n")
				}
			}
			for _, ct := range []string{"text/event-stream", "text/event-stream; charset=utf-8", "application/json", ""} {
				var want usageResult
				want.model, want.prompt, want.completion, want.total, want.ok = tokensFromBody(body.Bytes(), ct)
				got := usageThroughWriter(ct, body.Bytes(), r)
				Expect(got).To(Equal(want), fmt.Sprintf("run %d content type %q body %q", run, ct, body.String()))
			}
		}
	})

	It("keeps the whole body of a JSON response", func() {
		body := []byte(`{"model":"m","usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)
		got := usageThroughWriter("application/json", body, rand.New(rand.NewSource(1)))
		Expect(got).To(Equal(usageResult{model: "m", prompt: 3, completion: 5, total: 8, ok: true}))
	})
})

// BenchmarkUsageCapture writes a 500-chunk chat stream through the usage
// capture: the whole-body buffer the middleware used before, and
// usageBodyWriter.
func BenchmarkUsageCapture(b *testing.B) {
	chunk := []byte(`data: {"created":1795768536,"object":"chat.completion.chunk","id":"0d11e42a-18c8-40c4-9607-71aadfd6ee6d","model":"bench","choices":[{"index":0,"finish_reason":null,"delta":{"content":" word"}}]}` + "\n\n")
	b.Run("whole-body", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			w := &bodyWriter{ResponseWriter: newDiscardWriter("text/event-stream"), body: new(bytes.Buffer)}
			for range 500 {
				_, _ = w.Write(chunk)
			}
			tokensFromBody(w.body.Bytes(), "text/event-stream")
		}
	})
	b.Run("last-line", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			w := &usageBodyWriter{ResponseWriter: newDiscardWriter("text/event-stream")}
			for range 500 {
				_, _ = w.Write(chunk)
			}
			w.tokens("text/event-stream")
		}
	})
}
