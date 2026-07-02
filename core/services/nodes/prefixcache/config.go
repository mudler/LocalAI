package prefixcache

import (
	"fmt"
	"time"
)

// Config holds prefix-cache-aware routing settings. Per-model overrides
// (policy, abs/rel thresholds, min-match) live on ModelSchedulingConfig; TTL
// and window/depth are global-only.
type Config struct {
	GlobalPolicy        RoutePolicy
	MinPrefixMatch      float64       // ratio matched/total, [0,1]
	BalanceAbsThreshold int           // absolute in-flight slack
	BalanceRelThreshold float64       // relative load ratio, >= 1
	TTL                 time.Duration // idle-timeout for entries
	HalfLife            time.Duration // recency decay for cacheWeight
	WindowBytes         int           // chunk window size
	MaxDepth            int           // max trailing blocks hashed
	// PressureWindow is the rolling window over which forced-disturb events are
	// counted for the autoscale signal (see Pressure). Default 1 minute.
	PressureWindow time.Duration
	// PressureScaleThreshold is the minimum forced-disturb count within
	// PressureWindow that makes the reconciler treat the cache-warm replica as
	// saturated and scale up (subject to MaxReplicas and capacity). Default 1,
	// i.e. any sustained forced-disturb.
	PressureScaleThreshold int
}

func DefaultConfig() Config {
	return Config{
		GlobalPolicy:           RoutePolicyPrefixCache,
		MinPrefixMatch:         0.3,
		BalanceAbsThreshold:    2,
		BalanceRelThreshold:    1.5,
		TTL:                    5 * time.Minute,
		HalfLife:               2 * time.Minute,
		WindowBytes:            256,
		MaxDepth:               64,
		PressureWindow:         time.Minute,
		PressureScaleThreshold: 1,
	}
}

// validateThresholdBounds enforces the numeric bounds shared between the
// per-model override validator (ValidateThresholds) and Config.Validate:
// minMatch in [0,1]; absThr >= 0; relThr == 0 (inherit) or >= 1. It is the
// single source of truth for those bounds so the endpoint and the global
// config cannot drift apart.
func validateThresholdBounds(absThr int, relThr, minMatch float64) error {
	if minMatch < 0 || minMatch > 1 {
		return fmt.Errorf("prefixcache: min_prefix_match must be in [0,1], got %v", minMatch)
	}
	if absThr < 0 {
		return fmt.Errorf("prefixcache: balance_abs_threshold must be >= 0, got %d", absThr)
	}
	if relThr != 0 && relThr < 1 {
		return fmt.Errorf("prefixcache: balance_rel_threshold must be 0 (inherit) or >= 1, got %v", relThr)
	}
	return nil
}

// ValidateThresholds checks per-model override bounds. routePolicy must be one
// of "", "round_robin", "prefix_cache" (explicit allow-list - NOT ParsePolicy,
// which maps unknown to Default and would accept typos). minMatch in [0,1];
// absThr >= 0; relThr == 0 (inherit) or >= 1.
func ValidateThresholds(routePolicy string, absThr int, relThr, minMatch float64) error {
	switch routePolicy {
	case "", "round_robin", "prefix_cache":
	default:
		return fmt.Errorf(`prefixcache: route_policy must be one of "", "round_robin", "prefix_cache", got %q`, routePolicy)
	}
	return validateThresholdBounds(absThr, relThr, minMatch)
}

func (c Config) Validate() error {
	// Config.BalanceRelThreshold has no "inherit" sentinel - it is a concrete
	// global value that must be >= 1 - so pass 0 for relThr to the shared
	// numeric check and assert the >= 1 floor here separately.
	if err := validateThresholdBounds(c.BalanceAbsThreshold, 0, c.MinPrefixMatch); err != nil {
		return err
	}
	if c.BalanceRelThreshold < 1 {
		return fmt.Errorf("prefixcache: balance_rel_threshold must be >= 1, got %v", c.BalanceRelThreshold)
	}
	if c.WindowBytes <= 0 || c.MaxDepth <= 0 {
		return fmt.Errorf("prefixcache: window_bytes and max_depth must be > 0")
	}
	// TTL must be positive: it is the entry idle-lifetime and the eviction
	// ticker runs at TTL/2, so time.NewTicker would panic on TTL <= 0.
	if c.TTL <= 0 {
		return fmt.Errorf("prefixcache: ttl must be > 0, got %v", c.TTL)
	}
	return nil
}
