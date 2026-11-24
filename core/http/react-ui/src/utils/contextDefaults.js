// Context-window defaults the UI shows before a model sets its own.
//
// These mirror core/config/defaults.go. A spec in core/config/defaults_test.go
// fails when the two drift, so change both together.

// What a model runs with when nothing sets or estimates a context size
// (DefaultContextSize).
export const DEFAULT_CONTEXT_SIZE = 4096

// What the VRAM estimates and the pickers assume when a model sets no context
// size (DefaultEstimateContextSize). It is display-only.
export const DEFAULT_ESTIMATE_CONTEXT_SIZE = 32768

// A context size of 0, negative or not a number means "not set" in a form.
// -1 is the exception on the server (full trained context), but a form must
// not write it by accident, so the editor treats every non-positive value as
// unset and leaves the key out.
export function isUnsetContextSize(value) {
  if (value === '' || value === null || value === undefined) return true
  const n = Number(value)
  return !Number.isFinite(n) || n <= 0
}
