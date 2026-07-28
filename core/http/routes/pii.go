package routes

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// RegisterPIIRoutes wires the read-only PII audit endpoint. The
// detection itself runs request-side from the chat middleware
// (routes/openai.go) and the MITM input path, driven by per-model NER
// detectors; this endpoint is observation-side only.
//
// The legacy regex tier (pattern catalogue + per-pattern action editor
// + dry-run/decide oracles) was removed — policy now lives on each
// detector model's pii_detection block, so there is nothing global to
// list or mutate here.
func RegisterPIIRoutes(e *echo.Echo, app *application.Application) {
	if app.PIIEvents() == nil {
		e.GET("/api/pii/events", func(c echo.Context) error {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "PII subsystem unavailable",
			})
		})
		return
	}

	// GetPIIEventsEndpoint godoc
	// @Summary List recent middleware events
	// @Description The event log is shared between the PII filter and the MITM proxy: PII redactions, proxy_connect (intercept decisions), and proxy_traffic (per-request byte counts) all flow through the same store. Filter by kind to narrow the view. Admin-only when auth is on; available to the local user in single-user mode.
	// @Tags pii
	// @Produce json
	// @Param correlation_id query string false "Correlation ID join key"
	// @Param user_id query string false "User id"
	// @Param pattern_id query string false "Detector group id (e.g. ner:EMAIL, pattern:ANTHROPIC_KEY)"
	// @Param kind query string false "Event kind: pii | proxy_connect | proxy_traffic"
	// @Param origin query string false "Redaction origin: middleware | proxy | pii_analyze | pii_redact"
	// @Param limit query int false "Max events" default(100)
	// @Success 200 {object} map[string]interface{}
	// @Router /api/pii/events [get]
	e.GET("/api/pii/events", func(c echo.Context) error {
		viewer := resolveUsageUser(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		// Admin-only when auth is enabled. Local user has Role: admin.
		if viewer.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "admin access required"})
		}

		limit := 100
		if v := c.QueryParam("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		events, err := app.PIIEvents().List(c.Request().Context(), pii.ListQuery{
			CorrelationID: c.QueryParam("correlation_id"),
			UserID:        c.QueryParam("user_id"),
			PatternID:     c.QueryParam("pattern_id"),
			Kind:          pii.EventKind(c.QueryParam("kind")),
			Origin:        c.QueryParam("origin"),
			Limit:         limit,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list events"})
		}
		return c.JSON(http.StatusOK, map[string]any{"events": events})
	})

	// Synchronous redaction service: scan a string and either report the
	// detected entities (analyze) or apply the policy (redact). Unlike the
	// admin-only events log above, these are an inference-tier service gated
	// by the pii_filter feature (any authenticated user), so a client can use
	// LocalAI's PII engine without routing a full chat request through it.
	e.POST("/api/pii/analyze", localai.PIIAnalyzeEndpoint(app))
	e.POST("/api/pii/redact", localai.PIIRedactEndpoint(app))
}
