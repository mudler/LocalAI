// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
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
)

// The release sets these with -ldflags "-X main.guestImage=...". A build that
// does not has no guest image, and the isolated backends then say so.
var (
	// guestImage is the OCI reference, by digest, of the image the guest runs.
	guestImage string
	// runscSHA256 is the checksum of the runsc this release was tested with. When
	// it is set, a runsc with another checksum is not used.
	runscSHA256 string
)

// unsupported builds the error a constructor returns when the host cannot run
// a backend.
func unsupported(format string, a ...any) error {
	return fmt.Errorf("%w: %s", sandbox.ErrUnsupported, fmt.Sprintf(format, a...))
}

// runscPath finds runsc: next to the executable (the package carries it), then
// on PATH. When a checksum is pinned, only a runsc that matches is used.
func runscPath() (string, error) {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "runsc"))
	}
	if p, err := exec.LookPath("runsc"); err == nil {
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return "", unsupported("runsc was not found next to the add-on or on PATH")
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		if runscSHA256 != "" {
			sum, err := fileSHA256(c)
			if err != nil || sum != strings.ToLower(runscSHA256) {
				return "", unsupported("%s does not match the checksum this release pins for runsc", c)
			}
		}
		return c, nil
	}
	return "", unsupported("runsc was not found next to the add-on or on PATH")
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
			return chv.New(chv.Options{StateDir: filepath.Join(dataDir, "run", "kvm"), CacheDir: filepath.Join(dataDir, "images", "kvm"), DefaultImage: guestImage})
		},
		GVisor: func() (sandbox.Backend, error) {
			if runtime.GOOS != "linux" {
				return nil, unsupported("gVisor exists only on Linux")
			}
			if err := noImage(); err != nil {
				return nil, err
			}
			runsc, err := runscPath()
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
