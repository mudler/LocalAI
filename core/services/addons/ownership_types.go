// SPDX-License-Identifier: MIT
package addons

import (
	"net/url"
	"path/filepath"
)

// OwnerConfig is deployment configuration, never request-supplied authority.
// Shared filesystems and automatic reassignment are intentionally unsupported.
type OwnerConfig struct {
	InstanceID, OwnerID, OwnerAddress, StorageID, Root string
	SharedStorage                                      bool
}

func (c OwnerConfig) validate() error {
	u, e := url.Parse(c.OwnerAddress)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || c.InstanceID == "" || c.OwnerID == "" || c.StorageID == "" || c.SharedStorage {
		return ErrConfiguration
	}
	// Only the owner has a state root. A frontend keeps no add-on state, so it
	// has no root to name and nothing may read or create one.
	if c.InstanceID == c.OwnerID && !filepath.IsAbs(c.Root) {
		return ErrConfiguration
	}
	return nil
}

// ownerIdentity is what the identity file stores.
type ownerIdentity struct{ OwnerID, StorageID string }

type OwnerGuard struct{ Config OwnerConfig }