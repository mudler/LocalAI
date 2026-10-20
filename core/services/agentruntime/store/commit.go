// SPDX-License-Identifier: MIT
package store

import (
	"context"
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
		var err error
		out, err = applySettlement(tx, p, c, SettlementWrite{ExpectedRevision: expectedRevision, TurnID: turnID, Checkpoint: checkpoint, Events: events, Mode: CompleteTurn})
		return err
	})
	if err != nil {
		return Settlement{}, err
	}
	return out, nil
}
