//go:build !linux

package chv

import (
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// New returns an error that wraps sandbox.ErrUnsupported on every system but
// Linux: the backend needs /dev/kvm and the Linux host tools.
func New(opts Options) (*Backend, error) {
	return nil, fmt.Errorf("%w: the Cloud Hypervisor backend needs Linux", sandbox.ErrUnsupported)
}
