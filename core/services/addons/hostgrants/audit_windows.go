//go:build windows

// SPDX-License-Identifier: MIT

package hostgrants

import (
	"os"
)

// OpenAudit opens or creates the audit file for append. On Windows there is
// no O_NOFOLLOW; the owner root is trusted.
func OpenAudit(path string) (*Audit, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|noFollowFlag, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Audit{f: f}, nil
}
