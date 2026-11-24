//go:build !windows

package atomicfile

// SyncDenied is always false off Windows: a Sync through a read-only handle
// succeeds there. See sync_windows.go.
func SyncDenied(error) bool { return false }
