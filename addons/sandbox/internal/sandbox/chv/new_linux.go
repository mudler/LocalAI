//go:build linux

package chv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// geteuid is os.Geteuid. It is a variable so that a test can check the
// refusal of root.
var geteuid = os.Geteuid

// New checks the host and returns a Backend. It returns an error that wraps
// sandbox.ErrUnsupported when /dev/kvm, a binary or a minimum version is
// missing.
func New(opts Options) (*Backend, error) {
	if geteuid() == 0 {
		return nil, fmt.Errorf("%w: the backend must not run as root on the host: run it as an unprivileged user", sandbox.ErrUnsupported)
	}
	if opts.StateDir == "" || opts.CacheDir == "" {
		return nil, errors.New("chv: StateDir and CacheDir are required")
	}
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 10 * time.Second
	}
	if opts.StageLimit < 0 {
		return nil, errors.New("chv: StageLimit is negative")
	}
	if opts.DefaultImage == "" {
		opts.DefaultImage = DefaultImage
	}
	for _, d := range []*string{&opts.StateDir, &opts.CacheDir} {
		abs, err := filepath.Abs(*d)
		if err != nil {
			return nil, err
		}
		if strings.ContainsRune(abs, ',') {
			return nil, fmt.Errorf("chv: %q: a comma breaks the cloud-hypervisor options", abs)
		}
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return nil, err
		}
		*d = abs
	}
	// A Unix socket path has at most 107 bytes: <StateDir>/<id>/vsock.sock_65535.
	if n := len(opts.StateDir) + len("/0123456789ab/vsock.sock_65535"); n > 107 {
		return nil, fmt.Errorf("chv: StateDir %q is too long for socket paths (%d of 107 bytes)", opts.StateDir, n)
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: /dev/kvm: %v", sandbox.ErrUnsupported, err)
	}
	_ = f.Close()
	b := &Backend{opts: opts}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, t := range []struct {
		name string
		path *string
		dst  *string
		args []string
		skip bool
	}{
		{"cloud-hypervisor", &opts.Bin.CloudHypervisor, &b.bins.CloudHypervisor, []string{"--version"}, false},
		{"virtiofsd", &opts.Bin.Virtiofsd, &b.bins.Virtiofsd, []string{"--version"}, false},
		{"bwrap", &opts.Bin.Bwrap, &b.bins.Bwrap, []string{"--version"}, opts.NoBwrap},
		{"mke2fs", &opts.Bin.Mke2fs, &b.bins.Mke2fs, []string{"-V"}, false},
		{"debugfs", &opts.Bin.Debugfs, &b.bins.Debugfs, []string{"-V"}, false},
	} {
		if t.skip {
			continue
		}
		name := *t.path
		if name == "" {
			name = t.name
		}
		p, err := sandbox.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("%w: %s not found: %v", sandbox.ErrUnsupported, t.name, err)
		}
		out, err := exec.CommandContext(ctx, p, t.args...).CombinedOutput() // #nosec G204 -- a pinned helper program that the add-on resolved; the add-on builds the arguments
		if err != nil {
			return nil, fmt.Errorf("%w: %s %v: %v", sandbox.ErrUnsupported, p, t.args, err)
		}
		v, ok := parseVersion(out)
		if !ok {
			return nil, fmt.Errorf("%w: %s: no version in %q", sandbox.ErrUnsupported, p, firstLine(out))
		}
		if min := minimums[t.name]; v[0] < min[0] || (v[0] == min[0] && v[1] < min[1]) {
			return nil, fmt.Errorf("%w: %s %d.%d is older than %d.%d", sandbox.ErrUnsupported, t.name, v[0], v[1], min[0], min[1])
		}
		*t.dst = p
	}
	return b, nil
}
