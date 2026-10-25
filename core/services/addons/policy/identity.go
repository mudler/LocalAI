// SPDX-License-Identifier: MIT
package policy

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Mode is the ownership model of a store. It is deployment configuration, never
// request-supplied.
type Mode string

const (
	ModeNamedUser  Mode = "named-user"
	ModeLocalOwner Mode = "local-owner"
)

// LocalOwnerID is the single principal that owns every resource in
// local-owner mode.
const LocalOwnerID = "local-owner"

func (m Mode) valid() bool { return m == ModeNamedUser || m == ModeLocalOwner }

// synthetic reports IDs that name no person: the legacy shared API key, the
// anonymous caller and the local-owner placeholder.
func synthetic(id string) bool {
	l := strings.ToLower(id)
	return l == "anonymous" || l == LocalOwnerID || strings.HasPrefix(l, "legacy")
}

// validUserID is the shape check for a principal ID that may own or receive a
// grant in named-user mode.
func validUserID(id string) bool {
	return id != "" && len(id) <= 256 && strings.TrimSpace(id) == id && !synthetic(id)
}

// identity checks the actor against the store mode. Whether the account and
// credential are still valid is a separate, current check at admission.
func (m Mode) identity(a contract.Actor) error {
	if a.CredentialID == "" || len(a.CredentialID) > 256 || strings.TrimSpace(a.CredentialID) != a.CredentialID {
		return ErrForbidden
	}
	switch m {
	case ModeNamedUser:
		if a.Mode != string(ModeNamedUser) || !validUserID(a.UserID) {
			return ErrForbidden
		}
	case ModeLocalOwner:
		if a.Mode != string(ModeLocalOwner) || a.UserID == "" || len(a.UserID) > 256 {
			return ErrForbidden
		}
	default:
		return ErrForbidden
	}
	return nil
}

// ownerOf is the immutable owner recorded for a new resource.
func (m Mode) ownerOf(a contract.Actor) string {
	if m == ModeLocalOwner {
		return LocalOwnerID
	}
	return a.UserID
}
