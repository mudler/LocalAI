package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/routing/billing"
)

// RegisterUsageRoutes wires the routing-module billing endpoints. These
// are the auth-agnostic siblings of /api/auth/usage and
// /api/auth/admin/usage — they go through application.StatsRecorder()
// so that a no-auth single-user box also gets a working dashboard
// (the existing /api/auth/usage hardcodes a 401 when no user is on the
// context).
//
// Permission model:
//   - GET /api/usage          → current user's own usage; falls back to
//     the synthetic "local" user when auth is off.
//   - GET /api/usage/all      → cluster-wide; requires admin when auth
//     is on. In no-auth mode the local user is the only principal and
//     is treated as admin (the LocalUser is constructed with Role:
//     admin), so this endpoint returns the same data as /api/usage.
//
// Both endpoints accept ?period={day|week|month|all} (default month)
// and ?user_id=… on the admin path.
func RegisterUsageRoutes(e *echo.Echo, app *application.Application) {
	rec := app.StatsRecorder()
	if rec == nil {
		// Stats explicitly disabled (--disable-stats). Register stub
		// handlers that return 503 with a clear reason rather than
		// 404; clients (UI, MCP tools) can distinguish "not enabled
		// here" from "endpoint missing entirely".
		stub := func(c echo.Context) error {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"error": "usage tracking is disabled (--disable-stats)",
			})
		}
		e.GET("/api/usage", stub)
		e.GET("/api/usage/all", stub)
		return
	}

	// GetUsageEndpoint godoc
	// @Summary Get usage and token totals for the current user
	// @Description Returns time-bucketed token usage for the authenticated user. In single-user no-auth mode, returns usage for the synthetic "local" user. Pass ?period={day|week|month|all}.
	// @Tags usage
	// @Produce json
	// @Param period query string false "Time window: day, week, month, all" default(month)
	// @Success 200 {object} map[string]interface{}
	// @Router /api/usage [get]
	e.GET("/api/usage", func(c echo.Context) error {
		user := resolveUsageUser(c, app)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "not authenticated",
			})
		}

		period := c.QueryParam("period")
		if period == "" {
			period = "month"
		}

		buckets, err := rec.Aggregate(c.Request().Context(), billing.AggregateQuery{
			UserID: user.ID,
			Period: period,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to get usage",
			})
		}
		return c.JSON(http.StatusOK, usageResponse(buckets, user))
	})

	// GetAllUsageEndpoint godoc
	// @Summary Get cluster-wide usage (admin)
	// @Description Returns aggregate usage across all users. Requires admin role when auth is enabled. In single-user no-auth mode, returns the same data as /api/usage (the local user is the only principal).
	// @Tags usage
	// @Produce json
	// @Param period query string false "Time window: day, week, month, all" default(month)
	// @Param user_id query string false "Filter to a specific user"
	// @Success 200 {object} map[string]interface{}
	// @Failure 403 {object} map[string]string
	// @Router /api/usage/all [get]
	e.GET("/api/usage/all", func(c echo.Context) error {
		user := resolveUsageUser(c, app)
		if user == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "not authenticated",
			})
		}
		// Admin gate. The synthetic local user is built with Role: admin
		// in single-user mode, so this passes naturally when auth is off.
		if user.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{
				"error": "admin access required",
			})
		}

		period := c.QueryParam("period")
		if period == "" {
			period = "month"
		}
		filterUser := c.QueryParam("user_id")

		buckets, err := rec.Aggregate(c.Request().Context(), billing.AggregateQuery{
			UserID: filterUser, // empty = all users
			Period: period,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to get usage",
			})
		}
		return c.JSON(http.StatusOK, usageResponse(buckets, user))
	})
}

// resolveUsageUser returns the authenticated user when present,
// otherwise the synthetic local user when auth is off. Centralizes the
// "if not auth, fall back to local" pattern that both routes need.
func resolveUsageUser(c echo.Context, app *application.Application) *auth.User {
	if u := auth.GetUser(c); u != nil {
		return u
	}
	return app.FallbackUser()
}

// usageResponse builds the JSON shape the UI consumes. The "viewer"
// field surfaces who the data belongs to so a single-user dashboard
// can show "local" without inventing its own labels.
func usageResponse(buckets []auth.UsageBucket, viewer *auth.User) map[string]any {
	totals := auth.UsageTotals{}
	for _, b := range buckets {
		totals.PromptTokens += b.PromptTokens
		totals.CompletionTokens += b.CompletionTokens
		totals.TotalTokens += b.TotalTokens
		totals.RequestCount += b.RequestCount
	}
	resp := map[string]any{
		"usage":  buckets,
		"totals": totals,
	}
	if viewer != nil {
		resp["viewer"] = map[string]string{
			"id":       viewer.ID,
			"name":     viewer.Name,
			"role":     viewer.Role,
			"provider": viewer.Provider,
		}
	}
	return resp
}
