// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"errors"
)

// ErrTooMany means the list is longer than HardLimit.
var ErrTooMany = errors.New("too many pins")

// Service applies the visibility rules over a Store. `visible` is the set of
// add-on IDs the caller may see right now; the HTTP layer builds it from the
// built-in catalog and the Apps list, so a pin is filtered exactly like a tile.
type Service struct{ Store Store }

// List returns the caller's visible pins in order. It never seeds: the upgrade
// seed runs once at start (Store.SeedExisting), for the accounts that exist
// then, so an account created later starts with no pins.
func (s *Service) List(ctx context.Context, userID string, visible map[string]bool) ([]string, error) {
	stored, err := s.Store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, p := range stored {
		if visible[p.AddonID] {
			out = append(out, p.AddonID)
		}
	}
	return out, nil
}

// Put replaces the caller's visible pins with ids. Stored pins the caller
// cannot see are kept after them, so a save never erases a pin that is only
// hidden for now (a removed add-on inside its window, or an access change).
func (s *Service) Put(ctx context.Context, userID string, ids []string, visible map[string]bool) ([]string, error) {
	if len(ids) > HardLimit {
		return nil, ErrTooMany
	}
	seen := map[string]bool{}
	shown := []string{}
	for _, id := range ids {
		if visible[id] && !seen[id] {
			seen[id] = true
			shown = append(shown, id)
		}
	}
	stored, err := s.Store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	final := append([]string{}, shown...)
	for _, p := range stored {
		if !visible[p.AddonID] && !seen[p.AddonID] {
			seen[p.AddonID] = true
			final = append(final, p.AddonID)
		}
	}
	if err := s.Store.Replace(ctx, userID, final); err != nil {
		return nil, err
	}
	return shown, nil
}
