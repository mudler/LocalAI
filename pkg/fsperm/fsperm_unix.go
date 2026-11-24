//go:build !windows

package fsperm

import (
	"fmt"
	"io/fs"
	"os"
)

// noModeBits is false: Unix keeps privacy in the mode bits, and a loose
// mode is the choice of the owner.
const noModeBits = false

// Restrict removes the group and other bits from the mode of path. A file
// with mode 0644 gets 0600, a folder with mode 0755 gets 0700.
func Restrict(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	perm := st.Mode().Perm()
	if perm&0o077 == 0 {
		return nil
	}
	return os.Chmod(path, perm&^0o077) // #nosec G302 -- this removes bits, it never adds one
}

// Check returns nil when only the owner can open path. Otherwise it returns
// an error that wraps ErrNotPrivate and names the mode.
func Check(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s has mode %04o: %w (remove the group and other bits: chmod 600 for a file, chmod 700 for a folder)", path, perm, ErrNotPrivate)
	}
	return nil
}

// IsExecutable reports whether the file with this mode can run as a program.
// On Unix that is any execute bit. path is used on Windows only.
func IsExecutable(path string, mode fs.FileMode) bool {
	return isExecutable("linux", path, mode)
}
