package config

import (
	"fmt"
	"strings"

	"github.com/mudler/xlog"
)

// Serving-policy model-config defaults.
//
// Sibling to hardware_defaults.go: those fill values driven by the target
// *device* (Blackwell batch, VRAM-scaled parallel slots); these fill values
// that improve multi-request / multi-user *serving* regardless of the GPU. They
// run together from SetDefaults and only ever fill values the user left unset.

// DefaultCacheReuse is the minimum shared-prefix chunk (in tokens) the backend
// reuses across requests via KV-cache shifting. The llama.cpp backend ships this
// disabled (n_cache_reuse = 0); we enable it so repeated prefixes (system
// prompts, RAG context, agent scaffolds, multi-turn chat) are not recomputed.
// This is the universally-useful part of "paged attention" (cross-request prefix
// sharing) and needs none of the block-KV machinery.
const DefaultCacheReuse = 256

// ApplyServingDefaults fills serving-policy ModelConfig values the user left
// unset. Currently: enable cross-request prefix caching. Explicit
// cache_reuse/n_cache_reuse in the model options always wins.
func ApplyServingDefaults(cfg *ModelConfig) {
	if cfg == nil {
		return
	}
	// cache_reuse is a llama.cpp server option; a backend that strictly
	// validates its options rejects it. Only inject it on the llama.cpp path.
	if !UsesLlamaCppServingOptions(cfg.Backend) {
		return
	}
	if !backendOptionSet(cfg.Options, "cache_reuse", "n_cache_reuse") {
		cfg.Options = append(cfg.Options, fmt.Sprintf("cache_reuse:%d", DefaultCacheReuse))
		xlog.Debug("[serving_defaults] enabling cross-request prefix cache",
			"cache_reuse", DefaultCacheReuse)
	}
}

// backendOptionSet reports whether the backend options already set any of names.
// Options are "name:value" strings (or bare "name"); used so we never override
// an explicit value. Shared with hardware_defaults.go.
func backendOptionSet(opts []string, names ...string) bool {
	for _, o := range opts {
		name := o
		if i := strings.IndexByte(o, ':'); i >= 0 {
			name = o[:i]
		}
		name = strings.TrimSpace(strings.ToLower(name))
		for _, n := range names {
			if name == n {
				return true
			}
		}
	}
	return false
}
