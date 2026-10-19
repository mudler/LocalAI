// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"math"

	"gorm.io/gorm"
)

func (s *Store) Commit(ctx context.Context, p, c string, expectedRevision uint64, turnID string, checkpoint Checkpoint, events []Event) (Settlement, error) {
	if !validIDs(p, c, turnID) || checkpoint.Version > math.MaxInt64 {
		return Settlement{}, ErrInvalidInput
	}
	for _, e := range events {
		if !validIDs(e.Kind) || (e.TurnID != "" && e.TurnID != turnID) {
			return Settlement{}, ErrInvalidInput
		}
	}
	var out Settlement
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		r, err := load(tx, p, c)
		if err != nil {
			return err
		}
		if r.Revision != expectedRevision || r.RunningTurn != turnID {
			return ErrConflict
		}
		// Check capacity before arithmetic or SQL: both supported dialects use
		// signed 64-bit integer storage. Exhaustion is a state conflict.
		if r.Revision >= math.MaxInt64 || r.LastSequence > math.MaxInt64 || uint64(len(events)) > math.MaxInt64-r.LastSequence {
			return ErrConflict
		}
		var next inputRow
		err = scope(tx, p, c).Where("settled = ? AND input_id <> ?", false, turnID).Order("ordinal ASC").Take(&next).Error
		running := ""
		if err == nil {
			running = next.InputID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		err = cas(tx, r, map[string]any{
			"revision": r.Revision + 1, "running_turn": running, "last_sequence": r.LastSequence + uint64(len(events)),
			"checkpoint_version": checkpoint.Version, "checkpoint_context": checkpoint.Context, "checkpoint_artifacts": checkpoint.Artifacts, "checkpoint_pruning": checkpoint.Pruning, "checkpoint_transcript_cursor": checkpoint.TranscriptCursor,
		})
		if err != nil {
			return err
		}
		result := scope(tx.Model(&inputRow{}), p, c).Where("input_id = ? AND settled = ?", turnID, false).Update("settled", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		for i, e := range events {
			row := eventRow{Principal: p, Conversation: c, Sequence: r.LastSequence + uint64(i) + 1, TurnID: turnID, Kind: e.Kind, Payload: e.Payload}
			if err = tx.Create(&row).Error; err != nil {
				return err
			}
		}
		out = Settlement{Revision: r.Revision + 1}
		if running != "" {
			in := next.input()
			out.Next = &in
		}
		return nil
	})
	if err != nil {
		return Settlement{}, err
	}
	return out, nil
}
