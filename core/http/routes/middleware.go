package routes

import (
	"context"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/routing/router"
)

// RegisterMiddlewareRoutes wires the routing-module admin surface that
// powers the /app/middleware React page. Two endpoints:
//
//   - GET /api/middleware/status — single round-trip aggregator. Lists
//     PII patterns with current actions, each model's resolved
//     enabled/override state, recent event count, and a router status
//     stub (until subsystem 2 lands).
//   - GET /api/router/status — placeholder that the page renders for
//     the Routing tab. Returns { configured: false, models: [] } today;
//     subsystem 2 fills it in.
//
// Both are admin-only when auth is on. In single-user (no-auth) mode
// the synthetic local user has Role: admin so the page works without
// extra config — same gating shape as the existing /api/usage/all.
func RegisterMiddlewareRoutes(e *echo.Echo, app *application.Application) {
	e.GET("/api/middleware/status", func(c echo.Context) error {
		viewer := resolveUsageUser(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		if viewer.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "admin access required"})
		}

		piiSection := buildPIIStatus(app)
		routerSection := buildRouterStatus(app)
		mitmSection := buildMITMStatus(app)
		admissionSection := buildAdmissionStatus(app)

		return c.JSON(http.StatusOK, map[string]any{
			"pii":       piiSection,
			"router":    routerSection,
			"mitm":      mitmSection,
			"admission": admissionSection,
		})
	})

	e.GET("/api/router/status", func(c echo.Context) error {
		// Read-only — admins want to see classifier configurations
		// without authenticating, same as /api/pii/patterns.
		return c.JSON(http.StatusOK, buildRouterStatus(app))
	})

	e.GET("/api/middleware/proxy-ca.crt", func(c echo.Context) error {
		// The CA cert is the public half — safe to expose without
		// auth so clients can curl it during initial setup. The
		// private key never leaves disk and is mode 0600. Returning
		// 404 (rather than 500) when MITM is disabled keeps the
		// endpoint a clean "is this feature available?" probe.
		ca := app.MITMCA()
		if ca == nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "mitm proxy is not enabled (set --mitm-listen to start it)",
			})
		}
		c.Response().Header().Set("Content-Type", "application/x-pem-file")
		c.Response().Header().Set("Content-Disposition", `attachment; filename="localai-mitm-ca.crt"`)
		return c.Blob(http.StatusOK, "application/x-pem-file", ca.PublicCertPEM())
	})

	e.GET("/api/router/decisions", func(c echo.Context) error {
		viewer := resolveUsageUser(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		// Decision logs may include user ids — admin-only when auth is
		// on; the synthetic local user has admin so single-user mode
		// works.
		if viewer.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "admin access required"})
		}

		store := app.RouterDecisions()
		if store == nil {
			return c.JSON(http.StatusOK, map[string]any{"decisions": []any{}})
		}

		limit := 100
		if v := c.QueryParam("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		decisions, err := store.List(c.Request().Context(), router.DecisionListQuery{
			CorrelationID: c.QueryParam("correlation_id"),
			UserID:        c.QueryParam("user_id"),
			RouterModel:   c.QueryParam("router_model"),
			Limit:         limit,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list decisions"})
		}
		return c.JSON(http.StatusOK, map[string]any{"decisions": decisions})
	})

	// GET /api/router/cache/stats — embedding-cache counters per
	// router model. Read-only; same auth gating as /api/router/status
	// (any authenticated user can see configuration). Omitted entries
	// indicate "embedding cache not enabled for this router".
	e.GET("/api/router/cache/stats", func(c echo.Context) error {
		reg := app.RouterClassifierRegistry()
		stats := map[string]router.EmbeddingCacheStats{}
		if reg != nil {
			stats = reg.EmbeddingCacheStatsByRouter()
		}
		return c.JSON(http.StatusOK, map[string]any{"caches": stats})
	})

	// POST /api/router/decide — programmatic decision-oracle endpoint
	// for external routers. Runs the same classifier that the in-band
	// RouteModel middleware would have run and returns the chosen
	// label set + candidate model, without rewriting the request,
	// forwarding it, or recording a row in the decision store.
	//
	// Admin-only — same gating as /api/router/decisions. The risk
	// surface is "runs classifier inference on arbitrary input", which
	// matches the decision-log endpoint's gating.
	decideHandler := localai.RouterDecideEndpoint(
		app.ModelConfigLoader(),
		app.ApplicationConfig(),
		middleware.ClassifierDeps{
			Scorer:       app.Scorer,
			TokenCounter: app.TokenCounter,
			Embedder:     app.Embedder,
			VectorStore:  app.VectorStore,
			Reranker:     app.Reranker,
			ModelLookup:  app.ModelConfigLookup(),
			Registry:     app.RouterClassifierRegistry(),
			Evaluator:    app.TemplatesEvaluator(),
		},
	)
	e.POST("/api/router/decide", func(c echo.Context) error {
		viewer := resolveUsageUser(c, app)
		if viewer == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		}
		if viewer.Role != auth.RoleAdmin {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "admin access required"})
		}
		return decideHandler(c)
	})
}

// buildRouterStatus inventories every model that declares a Router
// block and reports their classifiers + candidate tables. Reads from
// the same loader the RouteModel middleware uses so the admin page
// agrees with what's actually live in the request path.
func buildRouterStatus(app *application.Application) map[string]any {
	models := []map[string]any{}
	hasAny := false
	cacheStats := map[string]router.EmbeddingCacheStats{}
	if reg := app.RouterClassifierRegistry(); reg != nil {
		cacheStats = reg.EmbeddingCacheStatsByRouter()
	}
	for _, cfg := range app.ModelConfigLoader().GetAllModelsConfigs() {
		if !cfg.HasRouter() {
			continue
		}
		hasAny = true
		candidates := make([]map[string]any, 0, len(cfg.Router.Candidates))
		for _, ca := range cfg.Router.Candidates {
			candidates = append(candidates, map[string]any{
				"model":  ca.Model,
				"labels": ca.Labels,
			})
		}
		policies := make([]map[string]any, 0, len(cfg.Router.Policies))
		for _, p := range cfg.Router.Policies {
			policies = append(policies, map[string]any{
				"label":       p.Label,
				"description": p.Description,
			})
		}
		classifier := cfg.Router.Classifier
		if classifier == "" {
			classifier = router.ClassifierScore
		}
		entry := map[string]any{
			"name":       cfg.Name,
			"classifier": classifier,
			"policies":   policies,
			"candidates": candidates,
			"fallback":   cfg.Router.Fallback,
		}
		if ec := cfg.Router.EmbeddingCache; ec != nil {
			cacheEntry := map[string]any{
				"embedding_model":      ec.EmbeddingModel,
				"similarity_threshold": ec.SimilarityThreshold,
				"confidence_threshold": ec.ConfidenceThreshold,
				"store_name":           ec.StoreName,
			}
			if s, ok := cacheStats[cfg.Name]; ok {
				cacheEntry["stats"] = s
			}
			entry["embedding_cache"] = cacheEntry
		}
		models = append(models, entry)
	}

	recentCount := 0
	if store := app.RouterDecisions(); store != nil {
		if n, err := store.Count(context.Background()); err == nil {
			recentCount = n
		}
	}

	out := map[string]any{
		"configured":            hasAny,
		"models":                models,
		"recent_decision_count": recentCount,
		"available_classifiers": []string{router.ClassifierScore},
	}
	if !hasAny {
		out["note"] = "No router models configured. Add a `router:` block to a model YAML to enable intelligent routing."
	}
	return out
}

func buildMITMStatus(app *application.Application) map[string]any {
	srv := app.MITMServer()
	ca := app.MITMCA()
	cfg := app.ApplicationConfig()

	// MITM-bound model configs — anything with an mitm: block, even
	// if hosts is empty. Surfaces a "fresh from template" config the
	// admin started but hasn't yet attached a host to.
	mitmModels := []map[string]any{}
	for _, mc := range app.ModelConfigLoader().GetModelConfigsByFilter(func(_ string, c *config.ModelConfig) bool {
		return len(c.MITM.Hosts) > 0
	}) {
		mitmModels = append(mitmModels, map[string]any{
			"name":        mc.Name,
			"hosts":       mc.MITM.Hosts,
			"pii_enabled": mc.PIIIsEnabled(),
			"backend":     mc.Backend,
		})
	}

	out := map[string]any{
		"running":         srv != nil,
		"listen_addr":     "",
		"configured_addr": cfg.MITMListen,
		"host_owners":     app.MITMHostOwners(),
		"host_conflicts":  app.MITMHostConflicts(),
		"models":          mitmModels,
		"ca_available":    ca != nil,
		"ca_cert_url":     "",
	}
	if conflicts := app.MITMHostConflicts(); len(conflicts) > 0 {
		out["error"] = "MITM listener disabled: duplicate host claims across model configs (see host_conflicts). Resolve by editing the conflicting model YAMLs so each host appears in at most one mitm.hosts list."
	}
	if srv != nil {
		out["listen_addr"] = srv.Addr()
	}
	if ca != nil {
		out["ca_cert_url"] = "/api/middleware/proxy-ca.crt"
	}
	return out
}

// buildAdmissionStatus reports each model's MaxConcurrent ceiling
// and current in-flight count. Models with no limit set are
// omitted — the dashboard view is "what's gated", not "every
// model in the loader".
func buildAdmissionStatus(app *application.Application) map[string]any {
	limiter := app.AdmissionLimiter()
	models := []map[string]any{}
	if limiter == nil {
		return map[string]any{"models": models}
	}
	for _, cfg := range app.ModelConfigLoader().GetAllModelsConfigs() {
		if cfg.Limits.MaxConcurrent <= 0 {
			continue
		}
		models = append(models, map[string]any{
			"name":                cfg.Name,
			"max_concurrent":      cfg.Limits.MaxConcurrent,
			"retry_after_seconds": cfg.Limits.RetryAfterSeconds,
			"in_flight":           limiter.InFlight(cfg.Name),
		})
	}
	return map[string]any{"models": models}
}

// buildPIIStatus builds the pii section of /api/middleware/status. It
// walks every model config and reports the resolved enabled state plus
// the NER detector models each one references — that's what the admin
// page renders so the operator can see at a glance which models are
// protected and by which detectors. The detection policy itself
// (entity→action, min score) lives on each detector model's
// pii_detection block.
func buildPIIStatus(app *application.Application) map[string]any {
	appCfg := app.ApplicationConfig()
	models := []map[string]any{}
	for _, cfg := range app.ModelConfigLoader().GetAllModelsConfigs() {
		// Only list models PII filtering can actually apply to (reachable
		// through a text-accepting endpoint with a PII adapter wired).
		// Skips VAD/STT/embedding/image-only models and the token_classify
		// detector models themselves, which are the filters, not consumers.
		if !cfg.PIIFilterApplies() {
			continue
		}
		explicit := cfg.PII.Enabled != nil
		ownDetectors := cfg.PIIDetectors()
		// Resolve through the shared policy so the table reflects the EFFECTIVE
		// state, including the instance-wide default detector — what the
		// request path actually does.
		enabled, detectors := app.ResolvePIIPolicy(&cfg)

		entry := map[string]any{
			"name":      cfg.Name,
			"backend":   cfg.Backend,
			"enabled":   enabled,
			"detectors": detectors,
			"explicit":  explicit,
			// Why is this on? backend default (cloud-proxy) vs an explicit YAML
			// toggle. Helps admins understand the resolved state without
			// reading source.
			"default_for_backend": !explicit && cfg.Backend == "cloud-proxy",
			// The detectors came from the global default, not this model's YAML.
			"detectors_from_default": enabled && len(ownDetectors) == 0 && len(detectors) > 0,
		}
		models = append(models, entry)
	}

	// Detector models: the token_classify "filter" models themselves (NER and
	// in-process pattern matchers), which PIIFilterApplies deliberately omits
	// from the consumer list above. The Filtering tab renders these as a table
	// with a per-row toggle marking membership in the instance-wide default
	// detector set, so admins manage defaults without retyping model names.
	defaultSet := map[string]bool{}
	for _, d := range appCfg.PIIDefaultDetectors {
		defaultSet[d] = true
	}
	detectorModels := []map[string]any{}
	for _, cfg := range app.ModelConfigLoader().GetAllModelsConfigs() {
		if !cfg.HasUsecases(config.FLAG_TOKEN_CLASSIFY) {
			continue
		}
		typ := "ner"
		if cfg.IsPatternDetector() {
			typ = "pattern"
		}
		detectorModels = append(detectorModels, map[string]any{
			"name":    cfg.Name,
			"backend": cfg.Backend,
			"type":    typ,
			// Whether this detector is in the instance-wide default set.
			"default": defaultSet[cfg.Name],
		})
		delete(defaultSet, cfg.Name)
	}
	// Surface any default detector that names a model that is no longer loaded
	// (or lost the token_classify usecase) so the admin can still toggle it off.
	for name := range defaultSet {
		detectorModels = append(detectorModels, map[string]any{
			"name":    name,
			"backend": "",
			"type":    "unknown",
			"default": true,
			"missing": true,
		})
	}

	recentCount := 0
	if app.PIIEvents() != nil {
		if n, err := app.PIIEvents().Count(context.Background()); err == nil {
			recentCount = n
		}
	}

	return map[string]any{
		"enabled_globally":             true,
		"default_enabled_for_backends": []string{"cloud-proxy"},
		"models":                       models,
		"detector_models":              detectorModels,
		"recent_event_count":           recentCount,
		// Instance-wide default policy (the Default PII policy editor).
		"default_detectors": appCfg.PIIDefaultDetectors,
	}
}
