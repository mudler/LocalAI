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
)

// WSL prepares the launch of an add-on whose manifest has execution "wsl2":
// a Linux executable that runs in a WSL 2 distribution of this Windows host.
type WSL interface {
	// Prepare checks that WSL 2 can run the package and readies the
	// distribution for one launch of it. An error says, for people, why
	// the add-on cannot run (no WSL, no distribution, version 1, ...).
	Prepare(ctx context.Context, pkg packages.PackageReceipt) (WSLLaunch, error)
}

// WSLLaunch is how to start one add-on in WSL 2.
type WSLLaunch struct {
	// Path and Args start the executable in the distribution, with the
	// single argument --localai-addon-v1 (wsl.exe and its arguments).
	Path string
	Args []string
	// Env is added to the fixed environment of Path. It must list the
	// variables that cross into WSL (WSLENV), the channel mode among them.
	Env []string
	// DataDir is the private data directory of the add-on as the add-on sees
	// it, and HostDataDir the same directory as this host sees it. The host
	// operations that write files for the add-on use HostDataDir. Both are
	// empty when the supervisor has no data root.
	DataDir, HostDataDir string
}

// ErrNoWSL is the refusal of a package with execution "wsl2" on a host that
// has no WSL support configured. It is an ErrRuntime.
var ErrNoWSL = errors.New("this LocalAI cannot run add-ons in WSL 2")

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
