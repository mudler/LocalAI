package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/routing/admission"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// AdmissionControl runs after RouteModel so the limit applies to the
// SERVED model — a router fanout that lands on a saturated downstream
// model gets rejected even though the requested router-model has slack.
//
// On reject: HTTP 503, Retry-After header, error JSON. An audit row
// goes into the shared event store under KindAdmission so admins see
// rejection rates alongside PII and proxy events.
//
// Models without limits.max_concurrent (the common case) hit a fast
// no-op path — Acquire returns immediately for max <= 0.
func AdmissionControl(limiter *admission.Limiter, events pii.EventStore) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cfg, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			if !ok || cfg == nil {
				return next(c)
			}
			max := cfg.Limits.MaxConcurrent
			release, ok := limiter.Acquire(cfg.Name, max)
			if !ok {
				retryAfter := admission.RetryAfter(cfg.Limits.RetryAfterSeconds)
				recordAdmissionRejection(events, cfg.Name, retryAfter)
				c.Response().Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				return c.JSON(http.StatusServiceUnavailable, map[string]any{
					"error": map[string]any{
						"type":    "admission_rejected",
						"message": fmt.Sprintf("model %q is at capacity (max_concurrent=%d); retry after %s", cfg.Name, max, retryAfter),
					},
				})
			}
			defer release()
			return next(c)
		}
	}
}

// admissionEventSeq scopes IDs across the process so rapid
// rejections under load get unique row IDs without coordinating
// with the rest of the event-store ID schemes.
var admissionEventSeq atomic.Uint64

func recordAdmissionRejection(events pii.EventStore, modelName string, retryAfter time.Duration) {
	if events == nil {
		return
	}
	statusCode := http.StatusServiceUnavailable
	durMS := retryAfter.Milliseconds()
	id := fmt.Sprintf("adm_%d_%s", admissionEventSeq.Add(1), randHex(4))
	_ = events.Record(context.Background(), pii.PIIEvent{
		ID:         id,
		Kind:       pii.KindAdmission,
		Host:       modelName,
		StatusCode: statusCode,
		DurationMS: durMS,
		CreatedAt:  time.Now().UTC(),
	})
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
