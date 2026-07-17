package model

import (
	"sort"
	"strings"
)

// preferredGGUFBackend is tried first when auto-detecting the backend for a
// GGUF model, since GGUF is overwhelmingly llama.cpp's native format.
const preferredGGUFBackend = "llama-cpp"

// llmCapableBackend reports whether the named backend can serve a text/LLM GGUF
// model. The backend capability table lives in core/config, which is a
// higher-level package that already imports pkg/model; importing it back here
// would form a core/config -> pkg/model -> core/config cycle. So core/config
// registers the predicate via RegisterLLMCapableBackendFunc instead (see #9287).
// When unset (e.g. a build that never imports core/config) GGUF capability
// filtering is skipped and auto-detect falls back to the deterministic set.
var llmCapableBackend func(name string) bool

// RegisterLLMCapableBackendFunc wires the LLM-capability predicate used by
// SelectAutoLoadBackends. It is called from core/config's init so pkg/model
// need not import core/config (see #9287).
func RegisterLLMCapableBackendFunc(fn func(name string) bool) {
	llmCapableBackend = fn
}

// SelectAutoLoadBackends returns the ordered, deterministic list of backend
// names to try when loading a model that declares no explicit backend.
//
// available is the set of installed backend names (unordered, as it comes from a
// Go map). modelFile is the model file name/path (may be empty).
//
// The trial loop in (*ModelLoader).Load picks the first backend whose gRPC
// LoadModel succeeds, so the order and membership of this list directly decide
// which backend wins. The previous implementation ranged a Go map (random
// order) with no filtering, so an unrelated installed backend such as the
// "opus" audio codec could win a GGUF/LLM model load (#9287).
//
// Behaviour:
//   - The result is always deterministically ordered, so auto-detect no longer
//     depends on map iteration order.
//   - For a GGUF model file the list is filtered to LLM-capable backends and
//     llama-cpp is placed first, so an incompatible audio/codec/image backend
//     can never win the trial loop.
//   - If filtering would leave no candidate, the full sorted set is returned
//     instead, so a model that previously loaded never becomes unloadable.
func SelectAutoLoadBackends(available []string, modelFile string) []string {
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)

	if !isGGUFModelFile(modelFile) {
		return sorted
	}

	// No capability predicate wired (core/config not linked in): skip filtering
	// rather than risk dropping a valid candidate.
	if llmCapableBackend == nil {
		return sorted
	}

	filtered := make([]string, 0, len(sorted))
	hasLlama := false
	for _, b := range sorted {
		if b == preferredGGUFBackend {
			hasLlama = true
			continue // added explicitly first below
		}
		if isLLMCapableBackend(b) {
			filtered = append(filtered, b)
		}
	}
	if hasLlama {
		filtered = append([]string{preferredGGUFBackend}, filtered...)
	}

	if len(filtered) == 0 {
		// Conservative fallback: no known LLM-capable backend is installed, so
		// rather than refuse to load, fall back to the previous behaviour of
		// trying every installed backend (now at least in a deterministic order).
		return sorted
	}
	return filtered
}

func isGGUFModelFile(modelFile string) bool {
	return strings.HasSuffix(strings.ToLower(modelFile), ".gguf")
}

// isLLMCapableBackend reports whether a backend is known to serve text/LLM
// models. Backends absent from the capability map (unknown) are treated as
// not LLM-capable here: for GGUF auto-detection we only want backends we can
// positively confirm handle LLMs, and the zero-candidate fallback keeps unknown
// setups working. Callers must ensure llmCapableBackend is non-nil.
func isLLMCapableBackend(name string) bool {
	return llmCapableBackend(name)
}
