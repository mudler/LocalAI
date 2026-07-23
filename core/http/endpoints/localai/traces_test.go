package localai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/trace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Traces Endpoints", func() {
	var app *echo.Echo

	BeforeEach(func() {
		app = echo.New()
		app.GET("/api/traces", GetAPITracesEndpoint())
		app.GET("/api/traces/:id", GetAPITraceEndpoint())
		app.POST("/api/traces/clear", ClearAPITracesEndpoint())
		app.GET("/api/backend-traces", GetBackendTracesEndpoint())
		app.GET("/api/backend-traces/:id", GetBackendTraceEndpoint())
		app.POST("/api/backend-traces/clear", ClearBackendTracesEndpoint())
	})

	It("should return API traces", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/traces", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("should clear API traces", func() {
		req := httptest.NewRequest(http.MethodPost, "/api/traces/clear", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNoContent))
	})

	It("should return backend traces", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/backend-traces", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("should clear backend traces", func() {
		req := httptest.NewRequest(http.MethodPost, "/api/backend-traces/clear", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNoContent))
	})

	It("advertises the paging metadata in response headers", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/traces", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Header().Get("X-Trace-Limit")).To(Equal(strconv.Itoa(DefaultTraceListLimit)))
		Expect(rec.Header().Get("X-Trace-Offset")).To(Equal("0"))
		Expect(rec.Header().Get("X-Total-Count")).ToNot(BeEmpty())
	})

	It("caps an oversized explicit limit", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/traces?limit=999999", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Header().Get("X-Trace-Limit")).To(Equal(strconv.Itoa(MaxTraceListLimit)))
	})

	It("404s an unknown trace ID", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/traces/does-not-exist", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})
})

// The backend trace buffer is a package-level ring buffer, so these specs run
// in their own Describe with an explicit clear to stay independent.
var _ = Describe("Backend traces payload bounding", func() {
	var app *echo.Echo

	// A payload of the shape that made the live /api/backend-traces response
	// 3.4 MB: every entry carries the full input text.
	const heavyText = 8192

	BeforeEach(func() {
		app = echo.New()
		app.GET("/api/backend-traces", GetBackendTracesEndpoint())
		app.GET("/api/backend-traces/:id", GetBackendTraceEndpoint())

		trace.ClearBackendTraces()
		trace.InitBackendTracingIfEnabled(500, 0)
		base := time.Now()
		for i := range 200 {
			trace.RecordBackendTrace(trace.BackendTrace{
				// Distinct timestamps keep the newest-first ordering (and
				// therefore the offset paging) deterministic.
				Timestamp: base.Add(time.Duration(i) * time.Millisecond),
				Type:      trace.BackendTraceLLM,
				ModelName: "test-model",
				Summary:   "summary " + strconv.Itoa(i),
				Body:      strings.Repeat("b", heavyText),
				Data:      map[string]any{"input_text": strings.Repeat("x", heavyText)},
			})
		}
		// Recording is asynchronous through a channel; wait for the buffer.
		Eventually(func() int { return len(trace.GetBackendTraces()) }).Should(Equal(200))
	})

	AfterEach(func() {
		trace.ClearBackendTraces()
	})

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("bounds the default list to DefaultTraceListLimit entries", func() {
		rec := get("/api/backend-traces")

		Expect(rec.Code).To(Equal(http.StatusOK))
		var out []map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out).To(HaveLen(DefaultTraceListLimit))
		Expect(rec.Header().Get("X-Total-Count")).To(Equal("200"))
	})

	It("omits the heavy body and data fields from list entries", func() {
		rec := get("/api/backend-traces")

		var out []map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out[0]).ToNot(HaveKey("body"))
		Expect(out[0]["data"]).To(BeNil())
		Expect(out[0]["summary"]).ToNot(BeEmpty())
		Expect(out[0]["id"]).ToNot(BeEmpty())
	})

	It("shrinks the polled payload by orders of magnitude", func() {
		bounded := get("/api/backend-traces").Body.Len()
		unbounded := get("/api/backend-traces?limit=0&full=true").Body.Len()

		Expect(unbounded).To(BeNumerically(">", 200*heavyText))
		Expect(bounded).To(BeNumerically("<", unbounded/100))
	})

	It("serves the full record from the per-trace endpoint", func() {
		var list []map[string]any
		Expect(json.Unmarshal(get("/api/backend-traces").Body.Bytes(), &list)).To(Succeed())
		id, _ := list[0]["id"].(string)
		Expect(id).ToNot(BeEmpty())

		rec := get("/api/backend-traces/" + id)

		Expect(rec.Code).To(Equal(http.StatusOK))
		var one map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &one)).To(Succeed())
		Expect(one["body"]).To(HaveLen(heavyText))
		data, ok := one["data"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(data["input_text"]).To(HaveLen(heavyText))
	})

	It("pages through the buffer with offset", func() {
		var first, second []map[string]any
		Expect(json.Unmarshal(get("/api/backend-traces?limit=10").Body.Bytes(), &first)).To(Succeed())
		Expect(json.Unmarshal(get("/api/backend-traces?limit=10&offset=10").Body.Bytes(), &second)).To(Succeed())

		Expect(first).To(HaveLen(10))
		Expect(second).To(HaveLen(10))
		Expect(second[0]["id"]).ToNot(Equal(first[0]["id"]))
	})
})
