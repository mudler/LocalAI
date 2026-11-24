//go:build linux || darwin

// SPDX-License-Identifier: MIT
package runtime

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ledgerNoFollow refuses a symbolic link in place of a ledger file.
const ledgerNoFollow = unix.O_NOFOLLOW

// checkStateRoot refuses a state root reached through a symbolic link, or
// one that other users can read.
func checkStateRoot(root string) error {
	resolved, e := filepath.EvalSymlinks(root)
	if e != nil || resolved != root {
		return ErrRuntime
	}
	info, e := os.Stat(root)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		return ErrRuntime
	}
	return nil
}

func lockLedger(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }

// syncLedgerDir makes the rename of the ledger file durable.
func syncLedgerDir(root string) error {
	d, e := os.Open(root) // #nosec G304 -- the add-on state root, validated when the ledger opened
	if e != nil {
		return e
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
