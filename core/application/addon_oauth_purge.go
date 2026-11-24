// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// oauthPurgePort adds one step to the purge of an add-on: the OAuth client an
// administrator approved for it goes with its data. An uninstall keeps the
// client, so a reinstall needs no new approval; only a purge of the data
// removes it. The store is read when the purge runs, because the studio
// opens it after the recovery port is made.
type oauthPurgePort struct {
	recovery.PolicyPort
	store func() connectors.Store
}

// Forget removes the records of the add-on, then its approved OAuth client.
func (p oauthPurgePort) Forget(ctx context.Context, addonID string) (int, error) {
	n, err := p.PolicyPort.Forget(ctx, addonID)
	if err != nil {
		return n, err
	}
	if s := p.store(); s != nil {
		if err := s.DeleteOAuthClient(ctx, addonID); err != nil {
			return n, err
		}
	}
	return n, nil
}
