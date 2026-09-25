package localai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Throttle operation endpoint", func() {
	It("rejects a missing rate query", func() {
		app := echo.New()
		app.POST("/api/operations/:jobID/throttle", ThrottleOperationEndpoint(nil))
		req := httptest.NewRequest(http.MethodPost, "/api/operations/job-1/throttle", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body).To(HaveKey("error"))
	})

	It("rejects an invalid rate", func() {
		app := echo.New()
		app.POST("/api/operations/:jobID/throttle", ThrottleOperationEndpoint(nil))
		req := httptest.NewRequest(http.MethodPost, "/api/operations/job-1/throttle?rate=bogus", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("rejects a valid rate for an unknown operation", func() {
		svc := galleryop.NewGalleryService(nil, nil)
		app := echo.New()
		app.POST("/api/operations/:jobID/throttle", ThrottleOperationEndpoint(svc))
		req := httptest.NewRequest(http.MethodPost, "/api/operations/nope/throttle?rate=2mb", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body["error"]).To(ContainSubstring("not found"))
	})

	It("throttles an active operation end-to-end", func() {
		svc := galleryop.NewGalleryService(nil, nil)
		svc.StoreRateLimiterForTest("job-1", &downloader.DynamicRateLimiter{})
		app := echo.New()
		app.POST("/api/operations/:jobID/throttle", ThrottleOperationEndpoint(svc))
		req := httptest.NewRequest(http.MethodPost, "/api/operations/job-1/throttle?rate=500kb", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var body map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body["success"]).To(BeTrue())

		// rate=0 removes the limit without error.
		req = httptest.NewRequest(http.MethodPost, "/api/operations/job-1/throttle?rate=0", nil)
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})
