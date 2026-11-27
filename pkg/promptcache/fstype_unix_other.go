//go:build unix && !linux && !darwin

package promptcache

import "golang.org/x/sys/unix"

// fsTypeOf does not know the filesystems of this platform: unknown is never
// a network filesystem, so it does not turn the tier off.
func fsTypeOf(string, *unix.Statfs_t) (string, bool) { return "", false }
