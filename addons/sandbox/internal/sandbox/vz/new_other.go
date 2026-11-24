//go:build !(darwin && arm64)

package vz

import (
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// New returns an error that wraps sandbox.ErrUnsupported on every system but
// macOS on Apple silicon: the backend needs Virtualization.framework.
func New(opts Options) (*Backend, error) {
	return nil, fmt.Errorf("%w: the Virtualization.framework backend needs macOS on Apple silicon", sandbox.ErrUnsupported)
}
