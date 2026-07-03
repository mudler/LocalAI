package config

import (
	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/xlog"
)

// swaCacheOptionNames lists the backend option keys that control the
// sliding-window-attention KV cache. If the user pinned any of these we leave
// the SWA cache alone instead of forcing swa_full.
var swaCacheOptionNames = []string{"swa_full", "n_swa"}

// HasSlidingWindowAttention reports whether the parsed GGUF describes a
// sliding-window-attention (SWA) model — Gemma 2/3, Cohere2, Llama 4 and the
// like. The gguf-parser library normalizes the per-architecture
// `<arch>.attention.sliding_window` metadata key into
// GGUFArchitecture.AttentionSlidingWindow, applying the same family-specific
// rules llama.cpp uses (e.g. Phi-3 carries the key but does not actually run
// SWA, and is normalized to 0). A non-zero window means the model interleaves
// SWA layers, so the returned size is also the diagnostic value we log.
func HasSlidingWindowAttention(f *gguf.GGUFFile) (uint64, bool) {
	if f == nil {
		return 0, false
	}
	w := f.Architecture().AttentionSlidingWindow
	return w, w > 0
}

// ApplySWAFullDefault enables the full-size SWA KV cache (swa_full:true) for a
// sliding-window model, unless the user already pinned an SWA cache option.
//
// Why: llama.cpp defaults to a reduced SWA KV cache sized to the sliding window
// (memory-light), but that reduced cache cannot preserve a prompt prefix across
// requests. So for SWA models the cross-request prefix cache we enable in
// serving_defaults.go (cache_reuse) is silently defeated — every turn
// reprocesses the entire prompt. Setting swa_full:true makes llama.cpp keep the
// full KV cache so the shared prefix is actually reused.
//
// The tradeoff is memory: the full SWA cache scales with context_size, so this
// is gated to models that are genuinely SWA (never applied to dense models,
// where it would only waste memory) and never overrides an explicit user
// choice. `slidingWindow` is the value read from the GGUF and is used only for
// the diagnostic log line.
func ApplySWAFullDefault(cfg *ModelConfig, slidingWindow uint64) {
	if cfg == nil || slidingWindow == 0 {
		return
	}
	if backendOptionSet(cfg.Options, swaCacheOptionNames...) {
		xlog.Debug("[swa] sliding-window model but an SWA cache option is already set; leaving user choice intact",
			"name", cfg.Name, "sliding_window", slidingWindow)
		return
	}
	cfg.Options = append(cfg.Options, "swa_full:true")
	xlog.Debug("[swa] enabling swa_full for sliding-window model so the cross-request prompt-prefix cache survives (reduced SWA cache cannot reuse a prefix across requests)",
		"name", cfg.Name, "sliding_window", slidingWindow)
}
