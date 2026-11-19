// SPDX-License-Identifier: MIT
package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/wsl"
)

// WSL prepares the launch of an add-on whose manifest has
// osSettings.windows.execution "wsl2": a Linux executable that runs in a WSL 2
// distribution of this Windows host.
type WSL interface {
	// Prepare checks that WSL 2 can run the package and readies the
	// distribution for one launch of it. An error says, for people, why
	// the add-on cannot run (no WSL, no distribution, version 1, ...).
	Prepare(ctx context.Context, pkg packages.PackageReceipt) (WSLLaunch, error)
}

// WSLLaunch is how to start one add-on in WSL 2: Path and Args start the
// executable with the single argument --localai-addon-v1 (wsl.exe and its
// arguments), Env is added to the fixed environment of Path and lists the
// variables that cross into WSL (WSLENV), the channel mode among them.
// DataDir is the private data directory as the add-on sees it, HostDataDir
// the same directory as this host sees it; the host operations that write
// files for the add-on use HostDataDir. Both are empty when the supervisor
// has no data root. wsl.Launcher prepares it on Windows.
type WSLLaunch = wsl.Launch

// ErrNoWSL is the refusal of a package with osSettings.windows.execution "wsl2"
// on a host that has no WSL support configured. It is an ErrRuntime.
var ErrNoWSL = errors.New("this LocalAI cannot run add-ons in WSL 2")

// StartRefusal is a launch that could not start for a reason meant for
// people, such as WSL 2 missing for an add-on that runs there. It is an
// ErrRuntime, and it wraps the cause.
type StartRefusal struct {
	// Reason says what is missing and what to do.
	Reason string
	cause  error
}

func (r *StartRefusal) Error() string   { return ErrRuntime.Error() + ": " + r.Reason }
func (r *StartRefusal) Unwrap() []error { return []error{ErrRuntime, r.cause} }

func refusal(cause error) *StartRefusal { return &StartRefusal{Reason: cause.Error(), cause: cause} }

// elfMagic starts every Linux executable.
var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

// linuxExecutablePath returns the executable of a package that runs in WSL:
// a regular Linux (ELF) program, reached through no link inside the package.
// The host does not run it; it only checks that it is the right kind of file.
func linuxExecutablePath(dir, rel string) (string, bool) {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return "", false
	}
	base, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return "", false
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || !samePath(resolved, filepath.Join(base, filepath.FromSlash(rel))) {
		return "", false
	}
	f, e := os.Open(path) // #nosec G304 -- the executable of the installed package, checked above
	if e != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, len(elfMagic))
	if _, e := io.ReadFull(f, head); e != nil || !bytes.Equal(head, elfMagic) {
		return "", false
	}
	return path, true
}

// samePath compares two cleaned paths; Windows paths ignore letter case.
func samePath(a, b string) bool {
	if goruntime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
