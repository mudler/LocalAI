// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/chv"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/gvisor"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/host"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
)

// The release sets this with -ldflags "-X main.guestImage=...". A build that
// does not has no guest image, and the isolated backends then say so.
var (
	// guestImage is the OCI reference, by digest, of the image the guest runs.
	guestImage string
)

// unsupported builds the error a constructor returns when the host cannot run
// a backend.
func unsupported(format string, a ...any) error {
	return fmt.Errorf("%w: %s", sandbox.ErrUnsupported, fmt.Sprintf(format, a...))
}

// runscPath finds runsc and returns the path to use. A runsc that ships with
// the add-on is checked against the checksum pinned for its architecture and
// copied, with its helpers, into the data directory as executable files: the
// package does not keep the executable bit, and the AppArmor profile of an
// administrator names a path that stays the same. A runsc in a system folder is the
// administrator's own and is used as it is.
func runscPath(dataDir string) (string, error) {
	if exe, err := os.Executable(); err == nil {
		p, err := installRunsc(filepath.Dir(exe), filepath.Join(dataDir, "runsc"), runtime.GOARCH, runscPins)
		if err == nil || !errors.Is(err, errNoPackagedRunsc) {
			return p, err
		}
	}
	p, err := sandbox.LookPath("runsc", filepath.Join(dataDir, "runsc"))
	if err != nil {
		return "", unsupported("runsc was not found next to the add-on or in a system folder")
	}
	return p, nil
}

// packagedKVMTools installs the KVM programs that ship with the add-on, and
// returns their paths. A build run from outside a package has none, and the
// backend then looks in the system folders.
func packagedKVMTools(dataDir string) (chv.Bins, error) {
	exe, err := os.Executable()
	if err != nil {
		return chv.Bins{}, nil
	}
	return installKVMTools(filepath.Dir(exe), filepath.Join(dataDir, "kvm-bin"), runtime.GOARCH, kvmPins)
}

// errNoPackagedRunsc says the add-on's package carries no runsc.
var errNoPackagedRunsc = errors.New("no runsc in the package")

// The runsc this release was tested with. package/runsc.json describes the
// download; a test keeps the two equal.
const runscRelease = "20261012.0"

var runscPins = map[string]string{
	"amd64": "56996c546dbafd5716e1d09e307a131d97eb3b6483c22a1178631751f3e0392f",
	"arm64": "5d08298bff7f2fb964566f09410f2d158502d63d496fcbedfbe9c1b6bd3132d7",
}

// installRunsc copies the runsc and the gvisor-bin folder of srcDir into dstDir
// when the runsc has the checksum pinned for arch.
func installRunsc(srcDir, dstDir, arch string, pins map[string]string) (string, error) {
	src := filepath.Join(srcDir, "runsc")
	if _, err := os.Stat(src); err != nil {
		return "", errNoPackagedRunsc
	}
	want := strings.ToLower(pins[arch])
	if want == "" {
		return "", unsupported("this release pins no runsc for %s", arch)
	}
	got, err := fileSHA256(src)
	if err != nil || got != want {
		return "", unsupported("%s does not match the checksum this release pins for runsc", src)
	}
	dst := filepath.Join(dstDir, "runsc")
	if have, err := fileSHA256(dst); err == nil && have == want && helpersPresent(srcDir, dstDir) {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Join(dstDir, "gvisor-bin"), 0o700); err != nil {
		return "", err
	}
	if err := copyExecutable(src, dst); err != nil {
		return "", err
	}
	// The tool that prints the AppArmor profile of this runsc. It sits next to
	// runsc so that the administrator's command can name it.
	if _, err := os.Stat(filepath.Join(srcDir, profileTool)); err == nil {
		if err := copyExecutable(filepath.Join(srcDir, profileTool), filepath.Join(dstDir, profileTool)); err != nil {
			return "", err
		}
	}
	entries, err := os.ReadDir(filepath.Join(srcDir, "gvisor-bin"))
	if err != nil {
		return "", unsupported("the package has no gvisor-bin folder next to runsc")
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := copyExecutable(filepath.Join(srcDir, "gvisor-bin", e.Name()), filepath.Join(dstDir, "gvisor-bin", e.Name())); err != nil {
				return "", err
			}
		}
	}
	return dst, nil
}

// profileTool is the name of the program that prints the AppArmor profile.
const profileTool = "gvisor-profile"

func helpersPresent(srcDir, dstDir string) bool {
	if _, err := os.Stat(filepath.Join(srcDir, profileTool)); err == nil {
		if _, err := os.Stat(filepath.Join(dstDir, profileTool)); err != nil {
			return false
		}
	}
	entries, err := os.ReadDir(filepath.Join(srcDir, "gvisor-bin"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			if _, err := os.Stat(filepath.Join(dstDir, "gvisor-bin", e.Name())); err != nil {
				return false
			}
		}
	}
	return true
}

// copyExecutable writes src to dst with mode 0755, through a temporary file.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return atomicfile.WriteFrom(dst, in, 0o755)
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// newSelector builds the choice of backend over the data directory.
func newSelector(dataDir string) (*backends.Selector, error) {
	noImage := func() error {
		if guestImage == "" {
			return unsupported("this build has no guest image set; the release process sets it")
		}
		return nil
	}
	return backends.Open(backends.Options{
		DataDir: dataDir,
		KVM: func() (sandbox.Backend, error) {
			if runtime.GOOS != "linux" {
				return nil, unsupported("KVM exists only on Linux")
			}
			if err := noImage(); err != nil {
				return nil, err
			}
			bins, err := packagedKVMTools(dataDir)
			if err != nil {
				return nil, err
			}
			return chv.New(chv.Options{
				StateDir:     filepath.Join(dataDir, "run", "kvm"),
				CacheDir:     filepath.Join(dataDir, "images", "kvm"),
				DefaultImage: guestImage,
				Bin:          bins,
				NoBwrap:      noBwrap(os.LookupEnv, inContainer(containerMarkers...)), //nolint:forbidigo // add-on entry point, a separate process: SANDBOX_NO_BWRAP is a setting of the host it runs on
			})
		},
		GVisor: func() (sandbox.Backend, error) {
			if runtime.GOOS != "linux" {
				return nil, unsupported("gVisor exists only on Linux")
			}
			if err := noImage(); err != nil {
				return nil, err
			}
			runsc, err := runscPath(dataDir)
			if err != nil {
				return nil, err
			}
			return gvisor.New(gvisor.Options{StateDir: filepath.Join(dataDir, "run", "gvisor"), CacheDir: filepath.Join(dataDir, "images", "gvisor"), Runsc: runsc, DefaultImage: guestImage})
		},
		Host: func() (sandbox.Backend, error) {
			return host.New(host.Options{Root: filepath.Join(dataDir, "run", "host"), Log: func(principal, command string) {
				// The record of what ran on the host, with the user it ran for.
				xlog.Warn("sandbox: host command", "user", principal, "command", command)
			}})
		},
	})
}

// settingsAdapter is what the settings tools act on: the choice, and the
// sandboxes that a change makes useless.
type settingsAdapter struct {
	*backends.Selector
	m *manager.Manager
}

func (s settingsAdapter) Reset() { s.m.Reset(context.Background()) }

// unavailable is the runner of an add-on whose manager did not start.
type unavailable struct{}

func (unavailable) Run(context.Context, manager.Request) (string, error) {
	return "This host cannot run commands.", nil
}
