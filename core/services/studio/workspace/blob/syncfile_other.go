// SPDX-License-Identifier: MIT
//go:build !darwin

package blob

import "os"

// barrierSync is a plain Sync on every platform but macOS: there, a Sync is
// already cheap enough and there is no weaker command to use.
func barrierSync(f *os.File) error { return f.Sync() }
