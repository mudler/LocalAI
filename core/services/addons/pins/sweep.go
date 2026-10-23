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
func Sweep(ctx context.Context, store Store, present []string, now time.Time) (int64, error) {
	if present == nil {
		return 0, nil
	}
	pinned, err := store.Distinct(ctx)
	if err != nil {
		return 0, err
	}
	var gone, back []string
	for _, id := range pinned {
		if slices.Contains(present, id) {
			back = append(back, id)
		} else {
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
