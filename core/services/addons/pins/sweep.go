// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"time"
)

// Sweep applies the retention rule. present is every add-on ID that exists
// right now: the built-in IDs and every installed add-on, enabled or not. A
// nil present means the owner did not answer, and then nothing changes: an
// outage must never expire a pin.
//
// A seeded pin (SeedIDs) is kept while its add-on was never installed: the
// upgrade pins Studio and Talk before an administrator installs them, and
// that may take longer than Retention. Once the add-on was present, its pin
// follows the rule like any other.
func Sweep(ctx context.Context, store Store, present []string, now time.Time) (int64, error) {
	if present == nil {
		return 0, nil
	}
	var seen []string
	for _, id := range SeedIDs {
		if slices.Contains(present, id) {
			seen = append(seen, id)
		}
	}
	if err := store.MarkInstalled(ctx, seen); err != nil {
		return 0, err
	}
	installed, err := store.Installed(ctx)
	if err != nil {
		return 0, err
	}
	pinned, err := store.Distinct(ctx)
	if err != nil {
		return 0, err
	}
	var gone, back []string
	for _, id := range pinned {
		switch {
		case slices.Contains(present, id):
			back = append(back, id)
		case slices.Contains(SeedIDs, id) && !slices.Contains(installed, id):
			// Not installed yet: no clock runs, and a mark an older sweep
			// set is cleared.
			back = append(back, id)
		default:
			gone = append(gone, id)
		}
	}
	if err := store.ClearMissing(ctx, back); err != nil {
		return 0, err
	}
	if err := store.MarkMissing(ctx, gone, now); err != nil {
		return 0, err
	}
	return store.DeleteMissingBefore(ctx, now.Add(-Retention))
}
