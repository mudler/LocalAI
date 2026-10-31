// SPDX-License-Identifier: MIT
//go:build !linux

package blob

import (
	"errors"
	"os"
)

// linkInto cannot link a file from outside dir without a path, so the caller
// copies the bytes. Only Linux can name an open folder to link into.
func linkInto(string, *os.Root, string) error { return errors.ErrUnsupported }
