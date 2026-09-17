package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	corebackend "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/services/nodes"
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

func TestApplyNoAvailableNodes(t *testing.T) {
	t.Run("maps ErrNoAvailableNodes to 503", func(t *testing.T) {
		// The scheduler wraps the sentinel in fmt.Errorf chains and via
		// errors.Join — errors.Is must still find it.
		wrapped := fmt.Errorf("routing model foo: %w",
			fmt.Errorf("no available nodes: %w",
				fmt.Errorf("no healthy nodes available: %w",
						errors.Join(nodes.ErrEvictionBusy, nodes.ErrNoAvailableNodes))))

		code := applyNoAvailableNodes(wrapped, http.StatusInternalServerError)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", code)
		}
	})

	t.Run("maps selector-mismatch chain to 503", func(t *testing.T) {
		wrapped := fmt.Errorf("routing model bar: %w",
			fmt.Errorf("no available nodes: %w",
				fmt.Errorf("no healthy nodes match selector for model bar: {\"gpu.vendor\":\"tpu\"}: %w",
					nodes.ErrNoAvailableNodes)))

		code := applyNoAvailableNodes(wrapped, http.StatusInternalServerError)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", code)
		}
	})

	t.Run("passes through unrelated errors unchanged", func(t *testing.T) {
		code := applyNoAvailableNodes(errors.New("database timeout"), http.StatusInternalServerError)
		if code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", code)
		}
	})
}
