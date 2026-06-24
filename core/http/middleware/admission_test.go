package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/routing/admission"
	"github.com/mudler/LocalAI/core/services/routing/pii"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// recordingStore captures admission rows so the test can assert
// the audit trail without standing up the full pii event store.
type recordingStore struct {
	mu     sync.Mutex
	events []pii.PIIEvent
}

func (r *recordingStore) Record(_ context.Context, e pii.PIIEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}
func (r *recordingStore) List(_ context.Context, _ pii.ListQuery) ([]pii.PIIEvent, error) {
	return nil, nil
}
func (r *recordingStore) Count(_ context.Context) (int, error) { return 0, nil }
func (r *recordingStore) Close() error                         { return nil }

func runAdmission(lim *admission.Limiter, store *recordingStore, cfg *config.ModelConfig, handler echo.HandlerFunc) (*httptest.ResponseRecorder, error) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
	mw := AdmissionControl(lim, store)
	err := mw(handler)(c)
	return rec, err
}

var _ = Describe("Admission", func() {
	It("allows when under limit", func() {
		lim := admission.New()
		cfg := &config.ModelConfig{Limits: config.LimitsConfig{MaxConcurrent: 2}}
		cfg.Name = "m"
		rec, err := runAdmission(lim, &recordingStore{}, cfg, func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("rejects when full", func() {
		// Saturate the limiter outside the middleware, then a request
		// at the same model gets 503 with a Retry-After header.
		lim := admission.New()
		release, ok := lim.Acquire("busy", 1)
		Expect(ok).To(BeTrue(), "setup acquire should succeed")
		defer release()

		cfg := &config.ModelConfig{Limits: config.LimitsConfig{MaxConcurrent: 1, RetryAfterSeconds: 3}}
		cfg.Name = "busy"
		store := &recordingStore{}
		handlerCalled := false
		rec, err := runAdmission(lim, store, cfg, func(c echo.Context) error {
			handlerCalled = true
			return c.String(http.StatusOK, "ok")
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(rec.Header().Get("Retry-After")).To(Equal("3"))
		Expect(handlerCalled).To(BeFalse(), "handler should not run when admission rejects")
		Expect(rec.Body.String()).To(ContainSubstring("admission_rejected"))
		Expect(store.events).To(HaveLen(1))
		Expect(store.events[0].Kind).To(Equal(pii.KindAdmission))
		Expect(store.events[0].Host).To(Equal("busy"), "audit row carries the model name")
	})

	It("no limit configured is no-op", func() {
		// MaxConcurrent=0 means unlimited — handler always runs and no
		// audit row is written even after many calls.
		lim := admission.New()
		cfg := &config.ModelConfig{}
		cfg.Name = "open"
		store := &recordingStore{}
		for i := 0; i < 10; i++ {
			rec, err := runAdmission(lim, store, cfg, func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Code).To(Equal(http.StatusOK))
		}
		Expect(store.events).To(BeEmpty())
	})

	It("releases after handler", func() {
		// One slot, two SEQUENTIAL requests: the second succeeds because
		// the first's release runs on handler return.
		lim := admission.New()
		cfg := &config.ModelConfig{Limits: config.LimitsConfig{MaxConcurrent: 1}}
		cfg.Name = "tight"
		for i := 0; i < 3; i++ {
			rec, err := runAdmission(lim, &recordingStore{}, cfg, func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Code).To(Equal(http.StatusOK))
		}
	})
})
