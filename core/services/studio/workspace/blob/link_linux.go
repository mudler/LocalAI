// SPDX-License-Identifier: MIT
//go:build linux

package blob

import (
	"os"

	"golang.org/x/sys/unix"
)

// linkInto hard-links src, a file outside dir, as name in dir. os.Root cannot
// do this: its Link takes both names inside the root. linkat(2) takes the open
// folder as a descriptor, so the new name is made in that very folder and no
// path above it is looked up again. name has no separator.
func linkInto(src string, dir *os.Root, name string) error {
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Linkat(unix.AT_FDCWD, src, int(f.Fd()), name, 0); err != nil {
		return &os.LinkError{Op: "link", Old: src, New: name, Err: err}
	}
	return nil
}
