package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	corebackend "github.com/mudler/LocalAI/core/backend"
)

func TestApplyBackendAdmission(t *testing.T) {
	t.Run("maps BackendAdmissionError to 429 with Retry-After", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := &corebackend.BackendAdmissionError{Limit: 4, RetryAfter: 3 * time.Second}
		code := applyBackendAdmission(err, http.StatusInternalServerError, c)

		if code != http.StatusTooManyRequests {
			t.Fatalf("expected 429, got %d", code)
		}
		if got := rec.Header().Get("Retry-After"); got != "3" {
			t.Fatalf("expected Retry-After 3, got %q", got)
		}
	})

	t.Run("passes through non-admission errors unchanged", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		code := applyBackendAdmission(errors.New("some other error"), http.StatusInternalServerError, c)
		if code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", code)
		}
		if got := rec.Header().Get("Retry-After"); got != "" {
			t.Fatalf("expected no Retry-After, got %q", got)
		}
	})
}
