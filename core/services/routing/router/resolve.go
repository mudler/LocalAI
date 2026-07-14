package router

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

// CandidateLoader resolves a candidate's model name to its parsed
// ModelConfig. The router calls it after MatchCandidate to load the
// resolved target so the caller (HTTP middleware or realtime handler)
// can dispatch against it.
//
// Defined as a function value rather than tied to *config.ModelConfigLoader
// so callers in tests can stub it without spinning up a real loader.
type CandidateLoader func(name string) (*config.ModelConfig, error)

// ResolveResult is the output of Resolve. It captures everything a
// caller needs to (a) dispatch the request against the chosen candidate,
// (b) record an audit row, and (c) decide whether to fall back to the
// classifier-error path.
type ResolveResult struct {
	// RouterModel is the router config's own name (the model the client asked for).
	RouterModel string

	// ChosenModel is the candidate the classifier picked, or
	// cfg.Router.Fallback when the classifier errored / no candidate
	// covered the active labels.
	ChosenModel string

	// ChosenConfig is the loaded ModelConfig for ChosenModel. The
	// caller dispatches against this — it has the right backend,
	// pipeline, etc.
	ChosenConfig *config.ModelConfig

	// Decision carries the classifier's labels/score/latency/cache
	// info. When UsedFallback is true, Decision.Labels is
	// []string{LabelFallback}.
	Decision Decision

	// Labels are the labels recorded against this decision — either
	// Decision.Labels or []string{LabelFallback} when the classifier
	// failed. Pulled out so callers don't have to special-case the
	// fallback path.
	Labels []string

	// ClassifierName is the Name() of the classifier that produced the
	// decision, or LabelFallback when classifier setup itself failed
	// and the fallback path ran without a working classifier.
	ClassifierName string

	// UsedFallback is true when the result came from cfg.Router.Fallback
	// rather than a classifier-picked candidate (classifier
	// build/Classify error or no candidate covered the active labels).
	UsedFallback bool
}

// Resolve runs the full classify → match → load pipeline for a router
// model config. It is transport-agnostic: callers pass a built
// classifier, a candidate loader, and a probe; Resolve returns a
// ResolveResult or an error if the resolved config violates invariants
// or the fallback can't be loaded.
//
// Errors returned here are *terminal* — the caller should surface them
// to the client. Classifier-error fallbacks are non-terminal and folded
// into ResolveResult.UsedFallback.
//
// classifier may be nil; that signals "classifier build failed" and
// pushes resolution straight to the fallback path (mirrors the
// classifier-build-error branch in the historical RouteModel middleware).
func Resolve(ctx context.Context, routerCfg *config.ModelConfig, classifier Classifier, loader CandidateLoader, probe Probe) (*ResolveResult, error) {
	if routerCfg == nil || !routerCfg.HasRouter() {
		return nil, fmt.Errorf("router.Resolve: config has no router block")
	}

	if classifier == nil {
		return resolveFallback(routerCfg, loader, Decision{}, LabelFallback, "classifier unavailable")
	}

	start := time.Now()
	decision, err := classifier.Classify(ctx, probe)
	if err != nil {
		return resolveFallback(routerCfg, loader, Decision{Latency: time.Since(start)}, classifier.Name(), "classifier error: "+err.Error())
	}

	candidate := MatchCandidate(routerCfg.Router.Candidates, decision.Labels)
	if candidate == "" {
		return resolveFallback(routerCfg, loader, decision, classifier.Name(), "no candidate covers labels: "+strings.Join(decision.Labels, ","))
	}

	candidateCfg, err := loader(candidate)
	if err != nil || candidateCfg == nil {
		return nil, fmt.Errorf("router candidate %q not loadable: %w", candidate, err)
	}
	if candidateCfg.HasRouter() {
		return nil, fmt.Errorf("router candidate %q is itself a router (depth-1 invariant)", candidate)
	}

	return &ResolveResult{
		RouterModel:    routerCfg.Name,
		ChosenModel:    candidate,
		ChosenConfig:   candidateCfg,
		Decision:       decision,
		Labels:         decision.Labels,
		ClassifierName: classifier.Name(),
		UsedFallback:   false,
	}, nil
}

// resolveFallback handles the three failure modes that fall through to
// cfg.Router.Fallback: classifier build failed, Classify returned an
// error, or no candidate covered the active labels. Returns an error
// when no fallback is configured — those translate to 503/500 at the
// HTTP layer.
//
// reason is included in the wrapped error for debugging; it's not
// surfaced to the client.
func resolveFallback(routerCfg *config.ModelConfig, loader CandidateLoader, decision Decision, classifierName, reason string) (*ResolveResult, error) {
	if routerCfg.Router.Fallback == "" {
		return nil, fmt.Errorf("router: %s and no fallback configured", reason)
	}
	candidateCfg, err := loader(routerCfg.Router.Fallback)
	if err != nil || candidateCfg == nil {
		return nil, fmt.Errorf("router fallback %q not loadable: %w", routerCfg.Router.Fallback, err)
	}
	if candidateCfg.HasRouter() {
		return nil, fmt.Errorf("router fallback %q is itself a router (depth-1 invariant)", routerCfg.Router.Fallback)
	}
	decision.Labels = []string{LabelFallback}
	return &ResolveResult{
		RouterModel:    routerCfg.Name,
		ChosenModel:    routerCfg.Router.Fallback,
		ChosenConfig:   candidateCfg,
		Decision:       decision,
		Labels:         []string{LabelFallback},
		ClassifierName: classifierName,
		UsedFallback:   true,
	}, nil
}

// ToDecisionRecord projects a ResolveResult into the persisted
// DecisionRecord shape. Centralised so the chat-side recordHTTPDecision
// and the realtime-side recorder can't drift in which Decision fields
// they copy through — a new field added to Decision only needs to be
// remembered here, not at every call site.
//
// id, correlationID, userID, and source are caller-supplied because
// they originate outside the routing pipeline (request ID generator,
// auth, entry-point dispatch).
func (r *ResolveResult) ToDecisionRecord(id, correlationID, userID, source string) DecisionRecord {
	return DecisionRecord{
		ID:                  id,
		CorrelationID:       correlationID,
		UserID:              userID,
		RouterModel:         r.RouterModel,
		RequestedModel:      r.RouterModel,
		ServedModel:         r.ChosenModel,
		Classifier:          r.ClassifierName,
		Label:               strings.Join(r.Labels, ","),
		Score:               r.Decision.Score,
		LatencyMs:           r.Decision.Latency.Milliseconds(),
		Cached:              r.Decision.Cached,
		CacheSimilarity:     r.Decision.CacheSimilarity,
		LabelScores:         r.Decision.LabelScores,
		ActivationThreshold: r.Decision.ActivationThreshold,
		Source:              source,
		CreatedAt:           time.Now().UTC(),
	}
}

// MatchCandidate picks the FIRST candidate whose Labels are a
// superset of the active label set. Admins order the candidates list
// smallest → largest, so a request that needs one label routes to
// the smallest capable model and one that needs multiple falls to
// the first bigger candidate that covers them all. Returns empty
// string when no candidate matches; the caller falls back.
func MatchCandidate(candidates []config.RouterCandidate, active []string) string {
	if len(active) == 0 {
		return ""
	}
	for _, c := range candidates {
		if labelSetCovers(c.Labels, active) {
			return c.Model
		}
	}
	return ""
}

// labelSetCovers returns true when every element of needed appears
// in have. Label sets are typically <10 entries so the linear scan
// is fine.
func labelSetCovers(have, needed []string) bool {
	for _, n := range needed {
		if !slices.Contains(have, n) {
			return false
		}
	}
	return true
}
