package model

import (
	"slices"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/config"
)

// preferredGGUFBackend is tried first when auto-detecting the backend for a
// GGUF model, since GGUF is overwhelmingly llama.cpp's native format.
const preferredGGUFBackend = "llama-cpp"

// llmCapableUsecases are the BackendCapabilities usecases that signal a backend
// can serve a text/LLM GGUF model. A GGUF model that declares no explicit
// backend must only be auto-tried against backends carrying one of these
// usecases - never against audio/codec/image backends (e.g. opus) that happen
// to be installed alongside it (see issue #9287).
var llmCapableUsecases = []string{
	config.UsecaseChat,
	config.UsecaseCompletion,
	config.UsecaseEdit,
	config.UsecaseEmbeddings,
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
// setups working.
func isLLMCapableBackend(name string) bool {
	capability := config.GetBackendCapability(name)
	if capability == nil {
		return false
	}
	for _, u := range capability.PossibleUsecases {
		if slices.Contains(llmCapableUsecases, u) {
			return true
		}
	}
	return false
}
