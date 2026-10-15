//go:build !windows

// SPDX-License-Identifier: MIT

package hostgrants

import (
	"os"
	"syscall"
)

// OpenAudit opens or creates the audit file for append. A symlink is refused.
func OpenAudit(path string) (*Audit, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|noFollowFlag, 0600) // #nosec G304 -- fixed file in the guarded owner root; NOFOLLOW rejects symlinks.
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Audit{f: f}, nil
}
