//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import "os"

func isNotExist(e error) bool { return os.IsNotExist(e) }
