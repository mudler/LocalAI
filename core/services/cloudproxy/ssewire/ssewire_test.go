package ssewire

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Scanner contract: returns one Event per double-newline-terminated
// SSE block, preserving the raw bytes (so unmodified events round-trip
// exactly) and extracting the first data: payload as DataLine.

var _ = Describe("Scanner", func() {
	It("scans a basic event", func() {
		in := "event: foo\ndata: hello\n\n"
		s := NewScanner(strings.NewReader(in))
		Expect(s.Scan()).To(BeTrue(), "Scan returned false on a well-formed event; err=%v", s.Err())
		ev := s.Event()
		Expect(ev.Raw).To(Equal(in))
		Expect(ev.DataLine).To(Equal("hello"))
		Expect(s.Scan()).To(BeFalse(), "Scan should return false after the only event")
	})

	It("handles CRLF", func() {
		// Some upstreams emit CRLF instead of LF. The scanner trims
		// trailing \r off the data line so DataLine carries the same
		// bytes whichever line ending the producer chose.
		in := "event: foo\r\ndata: hello\r\n\r\n"
		s := NewScanner(strings.NewReader(in))
		Expect(s.Scan()).To(BeTrue(), "Scan returned false on CRLF event; err=%v", s.Err())
		Expect(s.Event().DataLine).To(Equal("hello"))
	})

	It("scans multiple events", func() {
		in := "data: one\n\ndata: two\n\ndata: three\n\n"
		s := NewScanner(strings.NewReader(in))
		got := []string{}
		for s.Scan() {
			got = append(got, s.Event().DataLine)
		}
		Expect(got).To(Equal([]string{"one", "two", "three"}))
	})

	It("handles empty data payload", func() {
		// "data:" with no payload is valid SSE — DataLine should be empty
		// and Scan should still surface the event so callers can decide.
		in := "data:\n\n"
		s := NewScanner(strings.NewReader(in))
		Expect(s.Scan()).To(BeTrue(), "Scan returned false on empty data payload; err=%v", s.Err())
		Expect(s.Event().DataLine).To(Equal(""))
	})

	It("skips leading blank lines", func() {
		// A producer that prints a blank "keep-alive" before the first
		// real event must not produce a phantom event.
		in := "\n\n\ndata: real\n\n"
		s := NewScanner(strings.NewReader(in))
		Expect(s.Scan()).To(BeTrue(), "Scan returned false; err=%v", s.Err())
		Expect(s.Event().DataLine).To(Equal("real"))
	})

	It("handles mid-event EOF", func() {
		// EOF mid-event still surfaces the partial event with whatever
		// data was extracted — the StreamFilter+caller decides how to
		// handle a truncated upstream rather than silently dropping it.
		in := "data: half"
		s := NewScanner(strings.NewReader(in))
		Expect(s.Scan()).To(BeTrue(), "Scan returned false on partial event")
		ev := s.Event()
		Expect(ev.DataLine).To(Equal("half"))
		Expect(s.Scan()).To(BeFalse(), "Scan should not surface a second event after EOF")
	})
})

var _ = Describe("IsTerminalMarker", func() {
	cases := []struct {
		name     string
		dataLine string
		provider Provider
		want     bool
	}{
		{"openai DONE", "[DONE]", OpenAI, true},
		{"openai DONE with whitespace", "  [DONE]  ", OpenAI, true},
		{"anthropic DONE also recognised", "[DONE]", Anthropic, true},
		{"anthropic message_stop", `{"type":"message_stop"}`, Anthropic, true},
		{"anthropic content_block_delta is not terminal", `{"type":"content_block_delta"}`, Anthropic, false},
		{"openai chat.completion.chunk is not terminal", `{"object":"chat.completion.chunk"}`, OpenAI, false},
		{"openai message_stop is not terminal (wrong provider)", `{"type":"message_stop"}`, OpenAI, false},
		{"empty data", "", OpenAI, false},
		{"non-json garbage", "garbage", Anthropic, false},
	}
	for _, c := range cases {
		It(c.name, func() {
			Expect(IsTerminalMarker(c.dataLine, c.provider)).To(Equal(c.want))
		})
	}
})

var _ = Describe("SynthResidualEvent", func() {
	It("anthropic", func() {
		got := SynthResidualEvent(Anthropic, "tail")
		Expect(strings.HasPrefix(got, "event: content_block_delta\ndata:")).To(BeTrue(), "Anthropic residual event missing event/data lines: %q", got)
		Expect(strings.HasSuffix(got, "\n\n")).To(BeTrue(), "Anthropic residual event missing trailing blank line: %q", got)
		Expect(got).To(ContainSubstring(`"text":"tail"`))
	})

	It("openai", func() {
		got := SynthResidualEvent(OpenAI, "tail")
		Expect(strings.HasPrefix(got, "data: ")).To(BeTrue(), "OpenAI residual event missing data: prefix: %q", got)
		Expect(strings.HasSuffix(got, "\n\n")).To(BeTrue(), "OpenAI residual event missing trailing blank line: %q", got)
		Expect(got).To(ContainSubstring(`"content":"tail"`))
	})
})
