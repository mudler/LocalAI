package gallery

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrInvalidBackendName is the refusal of a backend name that cannot name a
// directory in the backends path.
var ErrInvalidBackendName = errors.New("invalid backend name")

// ValidateBackendName checks that name is one plain path element, so a path
// joined from it stays a directory of the backends path: not empty, no path
// separator, no "..", no leading dot (which also names the swap directories
// of other backends), no volume name (Windows) and no NUL. The install,
// upgrade and rollback code checks every name before it joins it into a
// path. A name from a request may also be an alias; the upgrade resolves it
// to the directory of the build first and checks that one.
func ValidateBackendName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: empty", ErrInvalidBackendName)
	case strings.ContainsAny(name, `/\`+"\x00"), filepath.VolumeName(name) != "":
		return fmt.Errorf("%w: %q has a path separator", ErrInvalidBackendName, name)
	case strings.Contains(name, ".."):
		return fmt.Errorf("%w: %q has \"..\"", ErrInvalidBackendName, name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("%w: %q starts with a dot", ErrInvalidBackendName, name)
	case isSwapDir(name):
		// The listing skips such a directory and a swap would take it for
		// the backup or the staging of another backend.
		return fmt.Errorf("%w: %q ends like a directory of an install or an upgrade", ErrInvalidBackendName, name)
	}
	return nil
}

// checkSwapPaths checks the two directories of a swap before a restore moves
// or removes anything: backendPath is a clean path whose last element is a
// valid backend name, and backupPath is that path with a swap suffix.
func checkSwapPaths(backendPath, backupPath string) error {
	if filepath.Clean(backendPath) != backendPath {
		return fmt.Errorf("%w: %q is not a clean path", ErrInvalidBackendName, backendPath)
	}
	if err := ValidateBackendName(filepath.Base(backendPath)); err != nil {
		return err
	}
	for _, suffix := range swapDirSuffixes {
		if backupPath == backendPath+suffix {
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a swap directory of %q", ErrInvalidBackendName, backupPath, backendPath)
}
