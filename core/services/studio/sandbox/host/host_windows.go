//go:build windows

package host

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

// Options configures the backend.
type Options struct {
	// Root is the folder the sandboxes live in. It must be an absolute path.
	Root string
	// Log is called before every command with the principal the sandbox runs
	// for and the command line. It is the record of what ran on the host.
	Log func(principal, command string)
}

// Backend runs commands on the host. The host backend runs POSIX shell
// commands in a process group, which Windows cannot provide, so New always
// reports the backend as unsupported there. Callers use
// errors.Is(err, sandbox.ErrUnsupported) to fall back to another backend.
type Backend struct{}

// New reports that the host backend is not available on Windows. Callers use
// errors.Is(err, sandbox.ErrUnsupported) to fall back to another backend.
func New(Options) (*Backend, error) {
	return nil, fmt.Errorf("%w: the host backend needs a POSIX process group and shell, which Windows does not provide", sandbox.ErrUnsupported)
}

// Skips lists what this backend cannot do, for the conformance suite.
func (*Backend) Skips(feature string) bool { return true }

func (*Backend) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, fmt.Errorf("%w: the host backend is not available on Windows", sandbox.ErrUnsupported)
}

func (*Backend) Resume(context.Context, sandbox.SnapshotRef, sandbox.Env) (sandbox.Sandbox, error) {
	return nil, fmt.Errorf("%w: the host backend is not available on Windows", sandbox.ErrUnsupported)
}

func (*Backend) Discard(context.Context, sandbox.SnapshotRef) error { return nil }

func (*Backend) Close() error { return nil }
