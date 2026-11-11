package config

import (
	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/core/config/harnessctx"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// contextFitHeadroomDivisor reserves a slice of per-device VRAM as headroom when
// deciding whether an auto-derived context fits. The gguf-parser footprint
// already covers weights + KV + compute buffer, but a live load also pays for
// allocator fragmentation, the CUDA/HIP context, and whatever else shares the
// card, so we require the estimate to leave at least 1/divisor of the device
// free. /5 (~20% headroom) mirrors the SWA full-cache gate's margin.
const contextFitHeadroomDivisor = 5

// contextFitCandidates is the descending ladder of context windows tried when
// the target window itself does not fit per-device VRAM. The steps from the
// recommended chat window down to the chat minimum come first, so a card that
// cannot hold 32768 still reaches 24576 or 16384 before anything below the
// minimum; 8192, 6144 and 4096 are used only when 16384 does not fit. The walk
// is capped at the base choice and floored at DefaultContextSize, and returns
// the first (largest) candidate that fits.
var contextFitCandidates = []int{
	harnessctx.RecommendedContext, harnessctx.MinContextAdmin, harnessctx.MinContext,
	8192, 6144, 4096,
}

// nonChatArchitectures are GGUF architectures that only embed or rerank text.
// A model of one of them never serves a chat, so it keeps the conservative
// window instead of the larger target of a chat model.
var nonChatArchitectures = map[string]bool{
	"bert": true, "nomic-bert": true, "nomic-bert-moe": true,
	"jina-bert-v2": true, "jina-bert-v3": true, "modern-bert": true,
	"neo-bert": true, "eurobert": true, "t5encoder": true,
}

// ServesChat reports whether a model may serve a chat, from what is known
// before it loads: its declared usecases and flags, and the architecture of
// its GGUF when the caller has it (empty when it does not). Only such a model
// gets the larger window of a chat; an embedding, rerank, score or other
// primitive-only model does not, because its window costs memory and buys it
// nothing.
func (c *ModelConfig) ServesChat(architecture string) bool {
	if c.Embeddings != nil && *c.Embeddings || c.Reranking != nil && *c.Reranking {
		return false
	}
	if reservedNonChatModel(c) || nonChatArchitectures[architecture] {
		return false
	}
	known := c.KnownUsecases
	if known == nil {
		known = GetUsecasesFromYAML(c.KnownUsecaseStrings)
	}
	if known != nil && *known != FLAG_ANY {
		// The operator declared what the model is for: chat must be in it.
		return *known&FLAG_CHAT != 0
	}
	return true
}

// AutoContextTarget is the window an unset context_size aims at: the
// recommended chat window for a model that can serve a chat, the conservative
// DefaultAutoContextSize for any other.
func (c *ModelConfig) AutoContextTarget(architecture string) int {
	if c.ServesChat(architecture) {
		return harnessctx.RecommendedContext
	}
	return DefaultAutoContextSize
}

// perDeviceVRAM reports the smallest per-GPU VRAM ceiling in bytes (0 = unknown
// or no GPU). It is a package var so tests can inject a deterministic value —
// detection does a live GPU probe. Per-device (not summed) is the right budget:
// with all layers offloaded to a single device the whole footprint must fit that
// one card, and a multi-GPU host is bounded by its smallest card. This mirrors
// localGPU's use of MinPerGPUVRAM in hardware_defaults.go.
var perDeviceVRAM = func() uint64 {
	v, _ := xsysinfo.MinPerGPUVRAM()
	return v
}

// estimateContextVRAM returns the estimated per-device VRAM footprint (bytes) of
// running f fully offloaded at ctx tokens — weights + KV cache + compute buffer.
// It returns 0 when it cannot produce an estimate (nil file, no tensors, or a
// parser panic), which the caller treats as "cannot confirm a smaller fit" and
// so keeps the conservative cap rather than clamping on a bogus number. It is a
// package var so tests can stub it (a fabricated GGUF carries no tensors and
// estimates to ~0).
var estimateContextVRAM = func(f *gguf.GGUFFile, ctx int) (footprint uint64) {
	if f == nil {
		return 0
	}
	if ctx <= 0 {
		ctx = DefaultContextSize
	}
	// The gguf-parser estimator panics on degenerate / partially-parsed GGUFs;
	// treat any failure as "unknown" so config loading never crashes on a model
	// the parser mis-handles.
	defer func() {
		if r := recover(); r != nil {
			xlog.Debug("[context_fit] per-device VRAM estimate failed; treating as unknown", "error", r)
			footprint = 0
		}
	}()
	// Offload all layers (LocalAI's DefaultNGPULayers default; the estimator
	// clamps to the model's block count) so the estimate reflects a fully
	// GPU-resident model. NonUMA is the discrete-GPU figure (larger than the UMA
	// one), which keeps the fit check conservative on unified-memory hosts — they
	// have ample memory to clear it anyway.
	est := f.EstimateLLaMACppRun(
		gguf.WithLLaMACppContextSize(int32(ctx)),
		gguf.WithLLaMACppOffloadLayers(uint64(DefaultNGPULayers)),
	)
	sum := est.Summarize(true, 0, 0)
	if len(sum.Items) == 0 {
		return 0
	}
	var total uint64
	for _, v := range sum.Items[0].VRAMs {
		total += uint64(v.NonUMA)
	}
	return total
}

// contextFitsVRAM reports whether an estimated footprint fits a per-device VRAM
// ceiling with headroom (VRAM must exceed the footprint by ~1/divisor). Unknown
// inputs (0) are treated as "cannot confirm" so a detection or estimate gap does
// not clamp the context.
func contextFitsVRAM(footprint, vram uint64) bool {
	if footprint == 0 || vram == 0 {
		return false
	}
	return vram >= footprint+footprint/contextFitHeadroomDivisor
}

// autoContextSize picks the default context to use when the user did not
// set context_size. The choice is deliberately conservative, NOT
// VRAM-maximizing:
//
//  1. Base cap: min(trainedMax, target). The target is the recommended chat
//     window (harnessctx.RecommendedContext) for a model that serves chats and
//     DefaultAutoContextSize for any other (see AutoContextTarget). A small
//     model keeps its trained window; a long-context model (128k / 256k / 1M)
//     is capped so its KV cache does not default to a size no consumer GPU can
//     hold. This applies always, including CPU / unknown-VRAM hosts.
//  2. VRAM is only a downward safety: when a per-device VRAM ceiling IS detected
//     and even the base cap would not fit it (with headroom), step down through
//     contextFitCandidates (32768, 24576, 16384, then 8192, 6144, 4096) to the
//     largest window that fits, floored at DefaultContextSize. When VRAM is
//     unknown we skip this: the base cap is already bounded and we must not
//     regress CPU / detection-gap hosts.
//
// footprint reports the per-device VRAM footprint of the model at a context; it
// is a parameter so the caller can supply a snapshot instead of the parsed file.
//
// trainedMax <= 0 means the estimate yielded nothing usable; the caller keeps
// its existing DefaultContextSize fallback in that case, so this is only called
// with a positive trainedMax.
func autoContextSize(footprint func(ctx int) uint64, trainedMax, target int) int {
	chosen := trainedMax
	if chosen > target {
		chosen = target
	}

	vram := perDeviceVRAM()
	if vram == 0 {
		// No per-device VRAM detected (CPU-only, unified memory reporting nothing,
		// or a detection gap). The bug is GPU OOM-on-load, so with no GPU budget to
		// reason about we must not clamp — the base cap already bounds long-context
		// models.
		return chosen
	}

	if contextFitsVRAM(footprint(chosen), vram) {
		return chosen
	}

	// The base cap does not fit this card. Walk candidates downward and take the
	// largest that fits, never below DefaultContextSize.
	for _, cand := range contextFitCandidates {
		if cand > chosen || cand < DefaultContextSize {
			continue
		}
		if contextFitsVRAM(footprint(cand), vram) {
			xlog.Debug("[context_fit] capped auto context to fit per-device VRAM",
				"context", cand, "base_cap", chosen, "vram_gib", vram>>30)
			return cand
		}
	}

	// Nothing fit (an unusually large model on a tiny card): fall back to the
	// floor. The backend still clamps n_gpu_layers to what fits, so a partial
	// offload can keep the model loadable rather than aborting outright.
	xlog.Debug("[context_fit] no candidate context fit per-device VRAM; using floor",
		"context", DefaultContextSize, "base_cap", chosen, "vram_gib", vram>>30)
	return DefaultContextSize
}
