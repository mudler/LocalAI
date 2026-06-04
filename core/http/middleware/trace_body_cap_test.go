package middleware

import (
	"bytes"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The trace middleware copies request and response bodies into an in-memory
// buffer that backs the admin /api/traces endpoint. With no upper bound a
// chatty workload (embeddings, large completions) trivially produces a
// multi-MB response that locks the Traces UI in a loading state — fetching
// and parsing the payload outruns the 5-second auto-refresh. These specs
// pin the capping contract so future refactors keep both the cap and the
// passthrough to the real client intact.

var _ = Describe("bodyWriter capping", func() {
	It("captures the full body when maxBytes is 0 (unlimited)", func() {
		downstream := httptest.NewRecorder()
		buf := &bytes.Buffer{}
		bw := &bodyWriter{ResponseWriter: downstream, body: buf, maxBytes: 0}

		payload := []byte(strings.Repeat("x", 4096))
		n, err := bw.Write(payload)

		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(len(payload)))
		Expect(buf.Len()).To(Equal(len(payload)))
		Expect(downstream.Body.Len()).To(Equal(len(payload)))
		Expect(bw.truncated).To(BeFalse())
	})

	It("stops appending to the trace buffer once maxBytes is reached but still forwards to the client", func() {
		downstream := httptest.NewRecorder()
		buf := &bytes.Buffer{}
		bw := &bodyWriter{ResponseWriter: downstream, body: buf, maxBytes: 100}

		payload := []byte(strings.Repeat("a", 250))
		n, err := bw.Write(payload)

		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(len(payload)), "Write must return the full byte count so callers see no short write")
		Expect(buf.Len()).To(Equal(100), "trace buffer should hold exactly maxBytes")
		Expect(downstream.Body.Len()).To(Equal(len(payload)), "client must still receive every byte")
		Expect(bw.truncated).To(BeTrue())
	})

	It("handles a write that straddles the cap by keeping only the leading slice", func() {
		downstream := httptest.NewRecorder()
		buf := &bytes.Buffer{}
		bw := &bodyWriter{ResponseWriter: downstream, body: buf, maxBytes: 10}

		_, err := bw.Write([]byte("12345"))
		Expect(err).ToNot(HaveOccurred())
		Expect(bw.truncated).To(BeFalse())

		_, err = bw.Write([]byte("67890ABCDE"))
		Expect(err).ToNot(HaveOccurred())

		Expect(buf.String()).To(Equal("1234567890"))
		Expect(downstream.Body.String()).To(Equal("1234567890ABCDE"))
		Expect(bw.truncated).To(BeTrue())
	})

	It("ignores further writes after the cap was already hit", func() {
		downstream := httptest.NewRecorder()
		buf := &bytes.Buffer{}
		bw := &bodyWriter{ResponseWriter: downstream, body: buf, maxBytes: 4}

		_, _ = bw.Write([]byte("AAAA"))
		_, _ = bw.Write([]byte("BBBB"))
		_, _ = bw.Write([]byte("CCCC"))

		Expect(buf.String()).To(Equal("AAAA"))
		Expect(downstream.Body.String()).To(Equal("AAAABBBBCCCC"))
		Expect(bw.truncated).To(BeTrue())
	})
})

var _ = Describe("truncateForTrace", func() {
	It("returns the input unchanged when below the cap", func() {
		in := []byte("hello")
		out, truncated := truncateForTrace(in, 1024)
		Expect(truncated).To(BeFalse())
		Expect(out).To(Equal(in))
	})

	It("truncates when the input exceeds the cap and signals truncation", func() {
		in := []byte(strings.Repeat("z", 200))
		out, truncated := truncateForTrace(in, 64)
		Expect(truncated).To(BeTrue())
		Expect(out).To(HaveLen(64))
		Expect(string(out)).To(Equal(strings.Repeat("z", 64)))
	})

	It("treats maxBytes <= 0 as unlimited (back-compat with current default)", func() {
		in := []byte(strings.Repeat("q", 10_000))
		out, truncated := truncateForTrace(in, 0)
		Expect(truncated).To(BeFalse())
		Expect(out).To(HaveLen(len(in)))
	})

	It("does not retain the caller's backing array (defensive copy)", func() {
		in := []byte("abcdefghij")
		out, truncated := truncateForTrace(in, 4)
		Expect(truncated).To(BeTrue())
		Expect(string(out)).To(Equal("abcd"))

		// Mutating the source must not corrupt the trace copy.
		in[0] = 'Z'
		Expect(string(out)).To(Equal("abcd"))
	})
})
