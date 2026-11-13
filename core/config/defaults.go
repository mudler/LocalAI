package config

import "github.com/mudler/LocalAI/core/config/harnessctx"

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
	// or estimable from the model. It is also the fallback for a GGUF whose
	// metadata yields no usable estimate or that the parser cannot read at all
	// (e.g. a quant type it does not know, such as NVFP4): a model-agnostic
	// safe default beats a tiny, surprising window that truncates real prompts.
	DefaultContextSize = 4096

	// DefaultAutoContextSize caps the context we auto-derive from a GGUF that
	// does not serve chats (embedding, rerank and other primitive-only models)
	// when the user did not set context_size. A model that serves chats aims at
	// harnessctx.RecommendedContext instead (see ModelConfig.AutoContextTarget).
	//
	// The importer used to default a model's context to its full trained window
	// (n_ctx_train). For long-context models (128k / 256k / 1M) that KV cache
	// cannot fit a consumer GPU and the backend aborts on load (exitCode=-1)
	// even though the model file is fine. So instead of shooting for the trained
	// max, we keep a modest default: a small model (trained < the target) keeps
	// its trained window, while a long-context model caps at the target. Users
	// who want the full window raise context_size explicitly. This is a
	// conservative default, not a VRAM-maximizing one: VRAM is only used to step
	// DOWN when even the target would not fit (see context_fit.go).
	DefaultAutoContextSize = 8192

	// DefaultEstimateContextSize is the context the VRAM estimators and the UI
	// assume when a model sets no context_size and the caller names none. It
	// follows the auto-context target of a chat model
	// (harnessctx.RecommendedContext) on purpose: a GGUF without an explicit value
	// is auto-sized up to that window, so "what will it use" and "what do we
	// estimate" must move together. A caller that has the model's config asks
	// ModelConfig.AutoContextTarget instead, which is lower for a model that
	// serves no chat. It is display-only and never sizes a load.
	DefaultEstimateContextSize = harnessctx.RecommendedContext

	// DefaultNGPULayers means "offload all layers"; the backend (fit_params)
	// clamps to what actually fits in device memory.
	DefaultNGPULayers = 99999999

	// DefaultFlashAttention is the flash-attention mode default; "auto" lets the
	// backend enable it when the model + backend support it.
	DefaultFlashAttention = "auto"
)
