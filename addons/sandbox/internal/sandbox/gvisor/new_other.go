//go:build !linux

package gvisor

import (
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// New reports that this system cannot run the backend: gVisor needs Linux.
func New(o Options) (*Backend, error) {
	return nil, fmt.Errorf("%w: the gVisor backend needs Linux", sandbox.ErrUnsupported)
}
