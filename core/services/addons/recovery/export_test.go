// SPDX-License-Identifier: MIT
package recovery

// ErrCrash makes an operation stop without its clean-up, as a dead process
// does. It exists only in test builds.
var ErrCrash = errCrash

// SetFailpoint installs the failure injection of a test. The function is
// called with the name of each phase and may fail it.
func (r *Recovery) SetFailpoint(f func(phase string) error) { r.fail = f }

// SetFree replaces the free-space probe of a test.
func (r *Recovery) SetFree(f func(path string) (uint64, error)) { r.free = f }
