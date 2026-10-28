// SPDX-License-Identifier: MIT
//go:build linux

package hostops

import (
	"fmt"
	"os"
)

// anchoredPath names the folder of r by the descriptor that r holds open, so
// a path-based API works on that very folder even if a path above it is
// swapped for a link later. release closes the descriptor.
func anchoredPath(r *os.Root) (string, func(), error) {
	f, err := r.Open(".")
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("/proc/self/fd/%d", f.Fd()), func() { _ = f.Close() }, nil
}
