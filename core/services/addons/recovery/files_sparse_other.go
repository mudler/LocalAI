//go:build !linux

// SPDX-License-Identifier: MIT
package recovery

import "os"

func copySparseData(in, out *os.File, size int64) error {
	return copyZeroBlocks(in, out, size)
}
