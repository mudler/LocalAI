package config

// Canonical default values.
//
// These are owned here so the two layers that need them share a single source
// of truth: the config tiers (ApplyInference/Hardware/Serving/Generic — which
// *decide* defaults) and core/backend/options.go (which *translates* a
// ModelConfig to the backend wire format and supplies the same fallbacks
// defensively). Previously these were duplicated as literals across both
// packages and had drifted (e.g. n_gpu_layers 9999999 vs 99999999, two batch
// constants of 512). core/backend imports core/config, so backend references
// these; config never imports backend.
const (
	// DefaultContextSize is the fallback context window when none is configured
	// or estimable from the model.
	DefaultContextSize = 4096

	// GGUFFallbackContextSize is the context window for a GGUF model whose
	// metadata yields no usable estimate (see guessGGUFFromFile). Deliberately
	// smaller than DefaultContextSize to stay conservative on memory there.
	GGUFFallbackContextSize = 1024

	// DefaultNGPULayers means "offload all layers"; the backend (fit_params)
	// clamps to what actually fits in device memory.
	DefaultNGPULayers = 99999999

	// DefaultFlashAttention is the flash-attention mode default; "auto" lets the
	// backend enable it when the model + backend support it.
	DefaultFlashAttention = "auto"
)
