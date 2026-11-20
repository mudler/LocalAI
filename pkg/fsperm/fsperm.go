// Package fsperm keeps files and folders private to the user that runs
// LocalAI, on every operating system.
//
// On Linux and macOS a private file has no group or other permission bits
// (mode 0600 for a file, 0700 for a folder). Windows has no permission bits:
// Go reports every writable file as 0666. There a private file has a protected
// access control list (DACL) that grants access to the current user only.
//
// Use Restrict after a file or folder is made, Check before a secret is read,
// and IsExecutable instead of a test of the execute bits.
package fsperm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotPrivate is returned by Check when other users can open the path.
var ErrNotPrivate = errors.New("other users can open it")

// windowsExecutableExts are the file extensions that Windows runs directly or
// through its default handler. Windows has no execute bit.
var windowsExecutableExts = map[string]bool{
	".exe": true,
	".com": true,
	".cmd": true,
	".bat": true,
	".ps1": true,
}

// isExecutable is the operating system rule behind IsExecutable. A test
// replaces goos to check the rule of the other systems.
func isExecutable(goos, path string, mode fs.FileMode) bool {
	if mode.IsDir() {
		return false
	}
	if goos == "windows" {
		return windowsExecutableExts[strings.ToLower(filepath.Ext(path))]
	}
	return mode&0o111 != 0
}

// WriteFile writes data to path like os.WriteFile. When perm has no group or
// other bits, it then makes the file private with Restrict, so the file is
// private on Windows too.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil { // #nosec G306 -- the caller chooses the mode; private modes are restricted below
		return err
	}
	if perm&0o077 != 0 {
		return nil
	}
	return Restrict(path)
}

// MkdirAll makes path and its parents like os.MkdirAll with mode 0700, then
// makes path private with Restrict, also when it already existed.
func MkdirAll(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return Restrict(path)
}

// CheckOrRestrict checks that path is private. When it is not, the result
// depends on the operating system:
//
//   - On Linux and macOS it returns the error of Check. The mode is the choice
//     of the owner, so LocalAI does not change it.
//   - On Windows it calls Restrict and returns restricted=true. LocalAI
//     versions before this package did not set an access control list, so an
//     old file has the list of its folder. Refusing it would break an upgrade.
func CheckOrRestrict(path string) (restricted bool, err error) {
	cerr := Check(path)
	if cerr == nil || !errors.Is(cerr, ErrNotPrivate) || !noModeBits {
		return false, cerr
	}
	if err := Restrict(path); err != nil {
		return false, fmt.Errorf("%w; making it private failed: %v", cerr, err)
	}
	return true, nil
}

// RequirePrivate checks a file or folder that the caller has already opened
// or read with Stat or Lstat. info is what the caller read.
//
//   - On Linux and macOS it checks the mode in info, so the answer is about the
//     object the caller holds and not about what the path names now. When
//     group or other bits are set it returns an error that wraps ErrNotPrivate.
//     It never changes the mode.
//   - On Windows info has no permission bits. It calls CheckOrRestrict on
//     path, so a file or folder made by an older LocalAI version, which has
//     the access control list of its folder, is made private and
//     restricted=true is returned.
func RequirePrivate(path string, info fs.FileInfo) (restricted bool, err error) {
	if noModeBits {
		return CheckOrRestrict(path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return false, fmt.Errorf("%s has mode %04o: %w", path, perm, ErrNotPrivate)
	}
	return false, nil
}

// RestrictIfPrivate gives path the private intent of perm on a system without
// permission bits. On Windows, when perm has no group or other bits, it calls
// Restrict. On Linux and macOS it does nothing, because the caller already
// set the mode. Use it after a Chmod or a create with an explicit mode.
func RestrictIfPrivate(path string, perm os.FileMode) error {
	if !noModeBits || perm&0o077 != 0 {
		return nil
	}
	return Restrict(path)
}
