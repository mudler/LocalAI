// SPDX-License-Identifier: MIT
//go:build !windows

package hostops

import "syscall"

// syscallNoFollow makes open fail on a symbolic link in the last element.
const syscallNoFollow = syscall.O_NOFOLLOW
