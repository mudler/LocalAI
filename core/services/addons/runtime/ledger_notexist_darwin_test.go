//go:build darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"errors"
	"io/fs"
)

func isNotExist(e error) bool { return errors.Is(e, fs.ErrNotExist) }
