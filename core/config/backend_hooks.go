package config

// BackendDefaultsHook is called during Prepare() and can modify cfg.
// Only fills in values that are not already set by the user.
type BackendDefaultsHook func(cfg *ModelConfig, modelPath string)

var backendHooks = map[string][]BackendDefaultsHook{}

// RegisterBackendHook registers a hook for a backend name.
// Special keys:
//   - "*"  = global catch-all, runs for EVERY backend (before specific hooks)
//   - ""   = runs only when cfg.Backend is empty (auto-detect case)
//   - "vllm", "llama-cpp" etc. = runs only for that specific backend
//
// Multiple hooks per key are supported; they run in registration order.
func RegisterBackendHook(backend string, hook BackendDefaultsHook) {
	backendHooks[backend] = append(backendHooks[backend], hook)
}

// runBackendHooks executes hooks in order:
//  1. "*" (global) hooks for every backend
//  2. Backend-specific hooks for cfg.Backend (includes "" when backend is empty)
func runBackendHooks(cfg *ModelConfig, modelPath string) {
	for _, h := range backendHooks["*"] {
		h(cfg, modelPath)
	}
	for _, h := range backendHooks[cfg.Backend] {
		h(cfg, modelPath)
	}
}
