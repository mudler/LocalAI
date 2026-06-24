package routes_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/routes"
)

var _ = Describe("Health and readiness probes", func() {
	var e *echo.Echo
	var ready atomic.Bool

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	BeforeEach(func() {
		e = echo.New()
		ready.Store(false)
		routes.HealthRoutes(e, ready.Load)
	})

	It("reports /readyz as unavailable while startup is in progress", func() {
		Expect(get("/readyz").Code).To(Equal(http.StatusServiceUnavailable))
	})

	It("reports /readyz as available once startup completes", func() {
		ready.Store(true)
		Expect(get("/readyz").Code).To(Equal(http.StatusOK))
	})

	It("keeps /healthz green during startup", func() {
		// Liveness and readiness answer different questions. Failing liveness
		// during a long preload would make Kubernetes restart the pod and the
		// preload would never finish.
		Expect(get("/healthz").Code).To(Equal(http.StatusOK))

		ready.Store(true)
		Expect(get("/healthz").Code).To(Equal(http.StatusOK))
	})

	It("treats a nil readiness source as ready", func() {
		// Fail open: an embedder that does not wire readiness keeps the
		// historical always-200 behaviour rather than being permanently
		// out of rotation.
		plain := echo.New()
		routes.HealthRoutes(plain, nil)

		rec := httptest.NewRecorder()
		plain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})
