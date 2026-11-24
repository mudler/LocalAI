//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package safefile

import (
	"errors"
	"fmt"
)

// ErrUnsafePath reports a path shape or file type that exact removal refuses.
var ErrUnsafePath = errors.New("unsafe removal path")

// RemoveExact fails closed on platforms without component-relative no-follow
// filesystem operations.
func RemoveExact(root, relativePath string, sidecarSuffixes []string, pruneParents int) error {
	return fmt.Errorf("secure exact removal is unsupported on this platform")
}
