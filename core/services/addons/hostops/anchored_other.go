// SPDX-License-Identifier: MIT
//go:build !linux

package hostops

import "os"

// anchoredPath names the folder of r by its path. Only Linux can name an open
// folder by its descriptor.
func anchoredPath(r *os.Root) (string, func(), error) {
	return r.Name(), func() {}, nil
}
