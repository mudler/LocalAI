//go:build !linux

package chv

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// The methods below let the add-on build on a system that cannot run the
// backend. New never returns a Backend there, so nothing calls them.

// Create reports that this system cannot run the backend.
func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, fmt.Errorf("%w: this backend needs Linux", sandbox.ErrUnsupported)
}

// Resume reports that this system cannot run the backend.
func (b *Backend) Resume(ctx context.Context, snap sandbox.SnapshotRef, env sandbox.Env) (sandbox.Sandbox, error) {
	return nil, fmt.Errorf("%w: this backend needs Linux", sandbox.ErrUnsupported)
}
