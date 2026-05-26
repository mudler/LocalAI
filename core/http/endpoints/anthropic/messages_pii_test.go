package anthropic

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// drainStreamPIIToText is called from four sites in messages.go and is
// the load-bearing primitive for "the streaming filter has buffered
// some bytes that the request just ended on; flush them as a final
// text_delta event before closing the content block". A regression
// here would silently truncate the last few bytes of an assistant
// response on every PII-enabled stream — invisible without coverage.

// newTestFilter compiles the default patterns and returns a filter
// that holds back its trailing pattern-window; pushing a short string
// (shorter than holdLen) keeps the bytes inside Drain.
func newTestFilter() *pii.StreamFilter {
	patterns, err := pii.Compile(pii.DefaultPatterns())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	red := pii.NewRedactor(patterns)
	return pii.NewStreamFilter(red, nil, nil, "", "")
}

// newTestContext builds a recording echo context — the recorder
// captures the SSE bytes drainStreamPIIToText writes.
func newTestContext() (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	return echo.New().NewContext(req, rec), rec
}

var _ = Describe("drainStreamPIIToText", func() {
	It("is a no-op when the filter is nil", func() {
		c, rec := newTestContext()
		drainStreamPIIToText(c, nil, intPtr(0))
		Expect(rec.Body.Len()).To(Equal(0), "nil filter wrote %d bytes: %q", rec.Body.Len(), rec.Body.String())
	})

	It("emits nothing when the drain is empty", func() {
		// A filter with nothing buffered should not emit a phantom event;
		// otherwise every non-PII response would close with an empty
		// text_delta that pollutes downstream parsers.
		sf := newTestFilter()
		c, rec := newTestContext()
		drainStreamPIIToText(c, sf, intPtr(0))
		Expect(rec.Body.Len()).To(Equal(0), "empty drain wrote %d bytes: %q", rec.Body.Len(), rec.Body.String())
	})

	It("flushes residual buffered bytes as a text_delta event", func() {
		sf := newTestFilter()
		// Push less than holdLen so all bytes are retained until Drain.
		// "tail" is short enough that no pattern is plausible.
		out := sf.Push("tail")
		Expect(out).To(Equal(""), "Push of short text emitted %q; want all bytes held", out)

		c, rec := newTestContext()
		drainStreamPIIToText(c, sf, intPtr(2))

		body := rec.Body.String()
		// Wire format: "event: content_block_delta\ndata: {…}\n\n"
		Expect(body).To(ContainSubstring("event: content_block_delta"))
		Expect(body).To(ContainSubstring(`"type":"content_block_delta"`))
		Expect(body).To(ContainSubstring(`"index":2`))
		Expect(body).To(ContainSubstring(`"text":"tail"`))
		Expect(body).To(ContainSubstring(`"type":"text_delta"`))
		Expect(strings.HasSuffix(body, "\n\n")).To(BeTrue(), "SSE event missing trailing blank line: %q", body)
	})

	It("is idempotent across consecutive drains", func() {
		// Two consecutive Drains: the filter returns "" the second time,
		// so the second drainStreamPIIToText must emit nothing. The
		// production path in messages.go has at least four call sites
		// that may overlap (currentBlockIndex==0 emergency path + the
		// unconditional drain near the end of the stream); without
		// idempotence we'd duplicate the residual on the wire.
		sf := newTestFilter()
		sf.Push("tail")

		c1, rec1 := newTestContext()
		drainStreamPIIToText(c1, sf, intPtr(0))
		first := rec1.Body.Len()
		Expect(first).NotTo(Equal(0), "first drain emitted nothing")

		c2, rec2 := newTestContext()
		drainStreamPIIToText(c2, sf, intPtr(0))
		Expect(rec2.Body.Len()).To(Equal(0), "second drain wrote %d bytes; want idempotent no-op: %q", rec2.Body.Len(), rec2.Body.String())
	})

	It("masks redacted residual instead of leaking it", func() {
		// The held tail must travel through the redactor on Drain. If
		// the bytes happen to form a complete pattern at end-of-stream,
		// the residual emit must contain the mask placeholder, not the
		// raw value.
		sf := newTestFilter()
		// "alice@example.com" is 17 bytes. holdLen for default patterns
		// is well above 17, so this stays buffered until Drain, which
		// then redacts it.
		out := sf.Push("alice@example.com")
		Expect(out).To(Equal(""), "Push emitted bytes early: %q", out)

		c, rec := newTestContext()
		drainStreamPIIToText(c, sf, intPtr(0))
		body := rec.Body.String()
		Expect(body).NotTo(ContainSubstring("alice@example.com"), "raw email leaked in residual emit: %q", body)
		Expect(body).To(ContainSubstring("[REDACTED:email]"), "residual emit missing mask placeholder: %q", body)
	})
})
