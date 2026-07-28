package middleware

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// /api/traces used to serialize the entire ring buffer, bodies included: a
// 1024-entry buffer of chat completions measured 21 MB on a live deployment,
// re-fetched by the admin UI every few seconds. These specs pin the two
// properties that make the poll cheap again: the list is bounded, and list
// entries carry no payload bodies.

func seedTraceBuffer(n, bodyBytes int) {
	body := []byte(strings.Repeat("x", bodyBytes))
	mu.Lock()
	traceBuffer = circularbuffer.New[APIExchange](n)
	base := time.Now()
	for i := range n {
		reqBody := make([]byte, len(body))
		copy(reqBody, body)
		resBody := make([]byte, len(body))
		copy(resBody, body)
		reqHeaders := http.Header{"Content-Type": {"application/json"}}
		resHeaders := http.Header{"Content-Type": {"application/json"}}
		traceBuffer.Enqueue(APIExchange{
			ID:        strconv.Itoa(i),
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Request: APIExchangeRequest{
				Method: "POST", Path: "/v1/chat/completions",
				Headers: &reqHeaders, Body: &reqBody, BodyBytes: len(reqBody),
			},
			Response: APIExchangeResponse{
				Status: 200, Headers: &resHeaders, Body: &resBody, BodyBytes: len(resBody),
			},
		})
	}
	mu.Unlock()
}

var _ = Describe("Trace pagination", func() {
	AfterEach(func() {
		mu.Lock()
		traceBuffer = nil
		mu.Unlock()
	})

	It("returns only the requested window and reports the true total", func() {
		seedTraceBuffer(200, 64)

		page, total := GetTracesPage(0, 50)

		Expect(total).To(Equal(200))
		Expect(page).To(HaveLen(50))
	})

	It("walks the buffer with offset", func() {
		seedTraceBuffer(20, 8)

		first, _ := GetTracesPage(0, 5)
		second, _ := GetTracesPage(5, 5)

		Expect(first).To(HaveLen(5))
		Expect(second).To(HaveLen(5))
		Expect(second[0].ID).ToNot(Equal(first[0].ID))
	})

	It("clamps an offset past the end to an empty page instead of panicking", func() {
		seedTraceBuffer(3, 8)

		page, total := GetTracesPage(500, 10)

		Expect(total).To(Equal(3))
		Expect(page).To(BeEmpty())
	})

	It("returns everything when the limit is zero", func() {
		seedTraceBuffer(17, 8)

		page, total := GetTracesPage(0, 0)

		Expect(total).To(Equal(17))
		Expect(page).To(HaveLen(17))
	})

	It("looks a trace up by ID with its body intact", func() {
		seedTraceBuffer(10, 32)

		found, ok := GetTrace("4")

		Expect(ok).To(BeTrue())
		Expect(found.ID).To(Equal("4"))
		Expect(found.Response.Body).ToNot(BeNil())
		Expect(*found.Response.Body).To(HaveLen(32))
	})

	It("reports a miss for an unknown ID", func() {
		seedTraceBuffer(2, 8)

		_, ok := GetTrace("nope")

		Expect(ok).To(BeFalse())
	})

	It("shrinks the serialized payload by dropping bodies and headers", func() {
		seedTraceBuffer(100, 4096)

		full, _ := GetTracesPage(0, 0)
		bounded, _ := GetTracesPage(0, 50)
		for i := range bounded {
			bounded[i] = SummarizeExchange(bounded[i])
		}

		fullJSON, err := json.Marshal(full)
		Expect(err).ToNot(HaveOccurred())
		boundedJSON, err := json.Marshal(bounded)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(boundedJSON)).To(BeNumerically("<", len(fullJSON)/50),
			"a bounded, summarized page must be orders of magnitude smaller than the full dump")
	})

	It("keeps the size counters after summarizing so the UI can still report payload sizes", func() {
		seedTraceBuffer(1, 1024)

		page, _ := GetTracesPage(0, 1)
		summary := SummarizeExchange(page[0])

		Expect(summary.Request.Body).To(BeNil())
		Expect(summary.Request.Headers).To(BeNil())
		Expect(summary.Response.Body).To(BeNil())
		Expect(summary.Response.Headers).To(BeNil())
		Expect(summary.Request.BodyBytes).To(Equal(1024))
		Expect(summary.Response.BodyBytes).To(Equal(1024))
		Expect(summary.Request.Path).To(Equal("/v1/chat/completions"))
		Expect(summary.Response.Status).To(Equal(200))
	})
})
