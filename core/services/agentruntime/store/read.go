// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

func (s *Store) Get(ctx context.Context, p, c string) (Snapshot, error) {
	if !validIDs(p, c) {
		return Snapshot{}, ErrInvalidInput
	}
	var out Snapshot
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		r, err := load(tx, p, c)
		if errors.Is(err, ErrDeleted) {
			out = Snapshot{Revision: r.Revision, Deleted: true}
			return err
		}
		if err != nil {
			return err
		}
		out = Snapshot{Revision: r.Revision, Checkpoint: r.Checkpoint}
		var inputs []inputRow
		if err = scope(tx, p, c).Where("settled = ?", false).Order("ordinal ASC").Find(&inputs).Error; err != nil {
			return err
		}
		for _, row := range inputs {
			in := row.input()
			if in.InputID == r.RunningTurn {
				out.Running = &in
			} else {
				out.Pending = append(out.Pending, in)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, ErrDeleted) {
		return Snapshot{}, err
	}
	return out, err
}

// Events pages immutable outbox rows, not a delivery acknowledgement. Deleted
// conversations do not expose retained private data through this API.
func (s *Store) Events(ctx context.Context, p, c string, after uint64, limit int) ([]Event, error) {
	if !validIDs(p, c) || limit < 1 || limit > MaxEventPage {
		return nil, ErrInvalidInput
	}
	var out []Event
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		if _, err := load(tx, p, c); err != nil {
			return err
		}
		var rows []eventRow
		if err := scope(tx, p, c).Where("sequence > ?", after).Order("sequence ASC").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		out = make([]Event, 0, len(rows))
		for _, r := range rows {
			out = append(out, Event{Sequence: r.Sequence, TurnID: r.TurnID, Kind: r.Kind, Payload: append([]byte(nil), r.Payload...)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
