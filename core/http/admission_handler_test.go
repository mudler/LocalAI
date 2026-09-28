package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	corebackend "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/services/nodes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend admission", func() {
	It("maps BackendAdmissionError to 429 with Retry-After", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := &corebackend.BackendAdmissionError{Limit: 4, RetryAfter: 3 * time.Second}
		code := applyBackendAdmission(err, http.StatusInternalServerError, c)

		Expect(code).To(Equal(http.StatusTooManyRequests))
		Expect(rec.Header().Get("Retry-After")).To(Equal("3"))
	})

	It("passes through non-admission errors unchanged", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		code := applyBackendAdmission(errors.New("some other error"), http.StatusInternalServerError, c)
		Expect(code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Header().Get("Retry-After")).To(BeEmpty())
	})
})

var _ = Describe("No available nodes", func() {
	It("maps ErrNoAvailableNodes to 503", func() {
		// The scheduler wraps the sentinel in fmt.Errorf chains and via
		// errors.Join — errors.Is must still find it.
		wrapped := fmt.Errorf("routing model foo: %w",
			fmt.Errorf("no available nodes: %w",
				fmt.Errorf("no healthy nodes available: %w",
					errors.Join(nodes.ErrEvictionBusy, nodes.ErrNoAvailableNodes))))

		code := applyNoAvailableNodes(wrapped, http.StatusInternalServerError)
		Expect(code).To(Equal(http.StatusServiceUnavailable))
	})

	It("maps selector-mismatch chain to 503", func() {
		wrapped := fmt.Errorf("routing model bar: %w",
			fmt.Errorf("no available nodes: %w",
				fmt.Errorf("no healthy nodes match selector for model bar: {\"gpu.vendor\":\"tpu\"}: %w",
					nodes.ErrNoAvailableNodes)))

		code := applyNoAvailableNodes(wrapped, http.StatusInternalServerError)
		Expect(code).To(Equal(http.StatusServiceUnavailable))
	})

	It("passes through unrelated errors unchanged", func() {
		code := applyNoAvailableNodes(errors.New("database timeout"), http.StatusInternalServerError)
		Expect(code).To(Equal(http.StatusInternalServerError))
	})
})
