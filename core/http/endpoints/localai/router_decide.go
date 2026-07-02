package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
)

// RouterDecideEndpoint exposes the routing classifier as a decision
// oracle: given a router model and a prompt, it runs the same
// classifier the in-band RouteModel middleware would have run, returns
// the active label set, and resolves which candidate model would have
// been picked. It does NOT rewrite anything, forward to a backend, or
// write to the decision store — Platform-side routers call this to get
// LocalAI's opinion without committing LocalAI to handle the request.
//
// The classifier is shared with the in-band middleware via the
// process-wide router.Registry on deps, so this endpoint and the
// request path agree on cache state, embedding-cache hits, etc.
//
// Takes discrete deps rather than the whole *application.Application so
// it stays unit-testable with a stub Scorer and a tmpdir-backed model
// loader (mirrors the existing route_model_test.go setup).
//
// @Summary  Classify a prompt against a router model's policies (decision oracle)
// @Tags     router
// @Accept   json
// @Produce  json
// @Param    request body schema.RouterDecideRequest true "decide params"
// @Success  200 {object} schema.RouterDecideResponse
// @Failure  400 {object} map[string]string
// @Failure  404 {object} map[string]string
// @Failure  500 {object} map[string]string
// @Failure  503 {object} map[string]string
// @Router   /api/router/decide [post]
func RouterDecideEndpoint(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, deps middleware.ClassifierDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.RouterDecideRequest
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: "+err.Error())
		}
		if req.Router == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "router is required")
		}
		if req.Input == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "input is required")
		}

		cfg, err := loader.LoadModelConfigFileByNameDefaultOptions(req.Router, appConfig)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to load model config: "+err.Error())
		}
		// LoadModelConfigFileByName returns a synthetic stub
		// (PredictionOptions.Model only, no Name) when neither an
		// in-memory config nor a YAML file exists for the requested
		// name. Use Name to discriminate "model unknown" (404) from
		// "model known but not a router" (400) — Platform wants both
		// signals.
		if cfg == nil || cfg.Name == "" {
			return echo.NewHTTPError(http.StatusNotFound, "router model not found: "+req.Router)
		}
		if !cfg.HasRouter() {
			return echo.NewHTTPError(http.StatusBadRequest, "model "+req.Router+" is not a router (no `router:` block)")
		}

		// Build (or reuse) the classifier via the same registry the
		// in-band middleware uses. Errors here are config problems —
		// classifier_model missing, policy without description, etc. —
		// so 503 is the right status: the router is configured but its
		// classifier can't be instantiated right now.
		classifier, err := middleware.GetOrBuildClassifier(deps.Registry, cfg, deps)
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "classifier unavailable: "+err.Error())
		}

		decision, err := classifier.Classify(c.Request().Context(), router.Probe{Prompt: req.Input})
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "classify failed: "+err.Error())
		}

		candidate := router.MatchCandidate(cfg.Router.Candidates, decision.Labels)
		fallback := false
		if candidate == "" && cfg.Router.Fallback != "" {
			candidate = cfg.Router.Fallback
			fallback = true
		}

		classifierName := cfg.Router.Classifier
		if classifierName == "" {
			classifierName = router.ClassifierScore
		}

		return c.JSON(http.StatusOK, schema.RouterDecideResponse{
			Router:            req.Router,
			Classifier:        classifierName,
			Labels:            decision.Labels,
			Candidate:         candidate,
			Fallback:          fallback,
			Score:             decision.Score,
			LatencyMs:         decision.Latency.Milliseconds(),
			Cached:            decision.Cached,
			CacheSimilarity:   decision.CacheSimilarity,
			NearestSimilarity: decision.NearestSimilarity,
		})
	}
}
