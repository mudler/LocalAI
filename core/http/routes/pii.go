package routes

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// RegisterPIIRoutes wires the read-only routing-PII endpoints. They
// surface (a) the active pattern set so admins can verify what is
// being filtered, (b) the recent PIIEvent log so they can audit what
// has been redacted, and (c) a dry-run "test" endpoint so an admin
// can paste candidate text and see what the redactor would do without
// sending a real request.
//
// The redactor itself runs from the chat middleware in routes/openai.go;
// these endpoints are observation- and configuration-side only.
func RegisterPIIRoutes(e *echo.Echo, app *application.Application) {
	if app.PIIRedactor() == nil {
		stub := func(c echo.Context) error {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "PII filter is disabled (--disable-pii)",
			})
		}
		e.GET("/api/pii/patterns", stub)
		e.GET("/api/pii/events", stub)
		e.POST("/api/pii/test", stub)
		e.POST("/api/pii/decide", stub)
		e.POST("/api/pii/patterns/persist", stub)
		return
	}

	// GetPIIPatternsEndpoint godoc
	// @Summary List the active PII patterns
	// @Description Returns the configured pattern set with their actions. Available without auth.
	// @Tags pii
	// @Produce json
	// @Success 200 {object} map[string]interface{}
	// @Router /api/pii/patterns [get]
	e.GET("/api/pii/patterns", func(c echo.Context) error {
		patterns := app.PIIRedactor().Patterns()
		out := make([]map[string]any, 0, len(patterns))
		for _, p := range patterns {
			out = append(out, map[string]any{
				"id":               p.ID,
				"description":      p.Description,
				"action":           string(p.Action),
				"disabled":         p.Disabled,
				"max_match_length": p.MaxMatchLength,
			})
		}
		return c.JSON(http.StatusOK, map[string]any{"patterns": out})
	})

	// GetPIIEventsEndpoint godoc
	// @Summary List recent middleware events
	// @Description The event log is shared between the PII filter and the MITM proxy: PII redactions, proxy_connect (intercept decisions), and proxy_traffic (per-request byte counts) all flow through the same store. Filter by kind to narrow the view. Admin-only when auth is on; available to the local user in single-user mode.
	// @Tags pii
	// @Produce json
	// @Param correlation_id query string false "Correlation ID join key"
	// @Param user_id query string false "User id"
	// @Param pattern_id query string false "Pattern id (e.g. email, ssn)"
	// @Param kind query string false "Event kind: pii | proxy_connect | proxy_traffic"
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
			Limit:         limit,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list events"})
		}
		return c.JSON(http.StatusOK, map[string]any{"events": events})
	})

	// PostPIITestEndpoint godoc
	// @Summary Dry-run the PII redactor against text
	// @Description Useful for admins tuning patterns. Returns the redacted text, matched spans, and whether the input would have been blocked.
	// @Tags pii
	// @Accept json
	// @Produce json
	// @Param body body map[string]string true "JSON {\"text\":\"...\"}"
	// @Success 200 {object} map[string]interface{}
	// @Router /api/pii/test [post]
	e.POST("/api/pii/test", func(c echo.Context) error {
		var body struct {
			Text string `json:"text"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		}
		res := app.PIIRedactor().Redact(body.Text)
		return c.JSON(http.StatusOK, map[string]any{
			"redacted":   res.Redacted,
			"spans":      res.Spans,
			"blocked":    res.Blocked,
			"local_only": res.LocalOnly,
		})
	})

	// POST /api/pii/decide — programmatic PII decision oracle for
	// external routers. Returns findings + suggested action without
	// mutating the caller's request or recording an audit event.
	// Production hot path — admin-only, matching /api/pii/events.
	decideHandler := localai.PIIDecideEndpoint(app.PIIRedactor())
	e.POST("/api/pii/decide", func(c echo.Context) error {
		viewer := resolveUsageUser(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		if viewer.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "admin access required"})
		}
		return decideHandler(c)
	})

	// PutPIIPatternActionEndpoint godoc
	// @Summary Change a pattern's action in-process
	// @Description Mutates the named pattern's action (mask|block|route_local). Transient — restored to YAML defaults on restart. Admin-only.
	// @Tags pii
	// @Accept json
	// @Produce json
	// @Param id path string true "Pattern id"
	// @Param body body map[string]string true "JSON {\"action\":\"mask|block|route_local\"}"
	// @Success 200 {object} map[string]interface{}
	// @Router /api/pii/patterns/{id} [put]
	e.PUT("/api/pii/patterns/:id", func(c echo.Context) error {
		viewer := resolveUsageUser(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		if viewer.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "admin access required"})
		}

		id := c.Param("id")
		if id == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "pattern id is required"})
		}
		// Either field is optional. The body must set at least one;
		// otherwise the call is a no-op and the client probably means
		// to PUT something.
		var body struct {
			Action   *string `json:"action,omitempty"`
			Disabled *bool   `json:"disabled,omitempty"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		}
		if body.Action == nil && body.Disabled == nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "must specify action and/or disabled"})
		}
		if body.Action != nil {
			if err := app.PIIRedactor().SetAction(id, pii.Action(*body.Action)); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
		}
		if body.Disabled != nil {
			if err := app.PIIRedactor().SetDisabled(id, *body.Disabled); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"id":        id,
			"action":    body.Action,
			"disabled":  body.Disabled,
			"persisted": false,
		})
	})

	// PostPIIPatternsPersistEndpoint godoc
	// @Summary Persist current pattern overrides to disk
	// @Description Snapshots the live redactor's per-pattern (action, disabled) state into runtime_settings.json so the next process start re-applies it. Admin-only. Pairs with PUT /api/pii/patterns/:id which only mutates in-process.
	// @Tags pii
	// @Produce json
	// @Success 200 {object} map[string]interface{}
	// @Router /api/pii/patterns/persist [post]
	e.POST("/api/pii/patterns/persist", func(c echo.Context) error {
		viewer := resolveUsageUser(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		if viewer.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "admin access required"})
		}

		appCfg := app.ApplicationConfig()
		existing, err := appCfg.ReadPersistedSettings()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "read settings: " + err.Error()})
		}
		// Only persist patterns whose live state differs from the YAML
		// default — that way an operator can compare runtime_settings.json
		// at a glance and see only the deltas they applied.
		defaults, dErr := pii.LoadConfig(appCfg.PIIConfigPath)
		if dErr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "reload defaults: " + dErr.Error()})
		}
		defaultByID := make(map[string]pii.Pattern, len(defaults))
		for _, d := range defaults {
			defaultByID[d.ID] = d
		}
		overrides := map[string]config.PIIPatternRuntimeOverride{}
		for _, p := range app.PIIRedactor().Patterns() {
			d, ok := defaultByID[p.ID]
			ov := config.PIIPatternRuntimeOverride{}
			changed := false
			if !ok || p.Action != d.Action {
				action := string(p.Action)
				ov.Action = &action
				changed = true
			}
			if !ok || p.Disabled != d.Disabled {
				disabled := p.Disabled
				ov.Disabled = &disabled
				changed = true
			}
			if changed {
				overrides[p.ID] = ov
			}
		}
		existing.PIIPatternOverrides = &overrides
		if err := appCfg.WritePersistedSettings(existing); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "write settings: " + err.Error()})
		}
		// Mirror onto the live ApplicationConfig so a subsequent reload
		// without a process restart sees the same map.
		appCfg.PIIPatternOverrides = overrides
		return c.JSON(http.StatusOK, map[string]any{
			"persisted":          true,
			"override_count":     len(overrides),
		})
	})
}
