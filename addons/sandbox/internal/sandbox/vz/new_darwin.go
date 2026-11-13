//go:build darwin && arm64

package vz

import (
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
)

// New checks the host and returns a Backend. It returns an error that wraps
// sandbox.ErrUnsupported when Virtualization.framework is missing or when the
// process does not hold the com.apple.security.virtualization entitlement.
//
// StateDir and CacheDir are created. New then takes an exclusive lock on
// StateDir (the file ".lock" in it), which it holds until Close. A second New
// on the same StateDir fails with a plain error (not ErrUnsupported).
//
// A VM stops when the process that started it ends. New therefore removes
// every session folder that an earlier process left: no guest runs in it
// any more. The suspended ones stay when Options.KeepSuspended is set.
func New(opts Options) (*Backend, error) {
	if err := vmm.Check(); err != nil {
		return nil, fmt.Errorf("%w: %v", sandbox.ErrUnsupported, err)
	}
	b, err := newBackend(opts, realMachine)
	if err != nil {
		return nil, err
	}
	b.host = vmm.Limits()
	return b, nil
}

// realMachine makes a Virtualization.framework machine.
func realMachine(c vmm.Config) (machine, error) {
	v, err := vmm.New(c)
	if err != nil {
		return nil, err
	}
	return v, nil
}
