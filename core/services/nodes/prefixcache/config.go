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
}

func DefaultConfig() Config {
	return Config{
		GlobalPolicy:        RoutePolicyPrefixCache,
		MinPrefixMatch:      0.3,
		BalanceAbsThreshold: 2,
		BalanceRelThreshold: 1.5,
		TTL:                 5 * time.Minute,
		HalfLife:            2 * time.Minute,
		WindowBytes:         256,
		MaxDepth:            64,
	}
}

func (c Config) Validate() error {
	if c.MinPrefixMatch < 0 || c.MinPrefixMatch > 1 {
		return fmt.Errorf("prefixcache: min_prefix_match must be in [0,1], got %v", c.MinPrefixMatch)
	}
	if c.BalanceAbsThreshold < 0 {
		return fmt.Errorf("prefixcache: balance_abs_threshold must be >= 0, got %d", c.BalanceAbsThreshold)
	}
	if c.BalanceRelThreshold < 1 {
		return fmt.Errorf("prefixcache: balance_rel_threshold must be >= 1, got %v", c.BalanceRelThreshold)
	}
	if c.WindowBytes <= 0 || c.MaxDepth <= 0 {
		return fmt.Errorf("prefixcache: window_bytes and max_depth must be > 0")
	}
	return nil
}
