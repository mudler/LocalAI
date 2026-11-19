//go:build linux

// SPDX-License-Identifier: MIT
package stdiomux

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Stdio takes descriptors 0 and 1 of this process for the add-on side of
// the session. Descriptor 1 then points at descriptor 2, so what the add-on
// prints on its standard output goes to the log and never into the session;
// descriptor 0 reads nothing.
func Stdio() (net.Conn, error) {
	in, err := unix.Dup(0)
	if err != nil {
		return nil, err
	}
	out, err := unix.Dup(1)
	if err != nil {
		_ = unix.Close(in)
		return nil, err
	}
	unix.CloseOnExec(in)
	unix.CloseOnExec(out)
	// Non-blocking descriptors join the poller of the runtime, so the
	// session can set write deadlines on them.
	_ = unix.SetNonblock(in, true)
	_ = unix.SetNonblock(out, true)
	null, err := unix.Open(os.DevNull, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err == nil {
		err = unix.Dup3(null, 0, 0)
		_ = unix.Close(null)
	}
	if err == nil {
		err = unix.Dup3(2, 1, 0)
	}
	if err != nil {
		_ = unix.Close(in)
		_ = unix.Close(out)
		return nil, err
	}
	return Conn(os.NewFile(uintptr(in), "stdin"), os.NewFile(uintptr(out), "stdout")), nil
}
