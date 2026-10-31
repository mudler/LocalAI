package http

import (
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/model"
)

// A model whose last load failed is answered 503 with Retry-After, and a header
// that tells in-process callers the failure is not a passing overload.
var _ = Describe("A model in load cooldown", func() {
	It("is marked as a failed load", func() {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), rec)
		code := applyModelLoadCooldown(&model.ModelLoadCooldownError{ModelID: "m", RetryAfter: 20 * time.Second}, http.StatusInternalServerError, c)
		Expect(code).To(Equal(http.StatusServiceUnavailable))
		Expect(rec.Header().Get(model.LoadFailedHeader)).To(Equal("true"))
	})
	It("names a missing backend as the cause", func() {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), rec)
		applyModelLoadCooldown(&model.ModelLoadCooldownError{ModelID: "m", RetryAfter: 20 * time.Second, BackendMissing: true}, http.StatusInternalServerError, c)
		Expect(rec.Header().Get(model.LoadCauseHeader)).To(Equal(model.LoadCauseBackendMissing))
	})
	It("leaves other errors unmarked", func() {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), rec)
		Expect(applyModelLoadCooldown(http.ErrAbortHandler, http.StatusInternalServerError, c)).To(Equal(http.StatusInternalServerError))
		Expect(rec.Header().Get(model.LoadFailedHeader)).To(BeEmpty())
	})
})
