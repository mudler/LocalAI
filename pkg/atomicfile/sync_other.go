//go:build !windows

package atomicfile

// dirSyncDenied is always false off Windows: a directory Sync failure there
// is real and must surface.
func dirSyncDenied(error) bool { return false }
