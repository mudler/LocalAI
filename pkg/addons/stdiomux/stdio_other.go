//go:build !linux

// SPDX-License-Identifier: MIT
package stdiomux

import (
	"errors"
	"net"
)

// Stdio refuses: the stdio mode is for a Linux add-on in WSL 2.
func Stdio() (net.Conn, error) {
	return nil, errors.New("the stdio channel mode needs Linux (an add-on that runs in WSL 2)")
}
