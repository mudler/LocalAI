// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"math"

	"gorm.io/gorm"
)

// ApplySettlement is a trusted transaction participant, NOT authority. The caller
// owns an already-open transaction and must roll it back on any error. It never
// opens, retries or commits a transaction. Runtime callers use ownership.Settle.
// The candidate is not a handoff until the caller's outer commit succeeds.
func ApplySettlement(tx *gorm.DB, p, c string, w SettlementWrite) (Settlement, error) {
	if tx == nil || !validSettlement(p, c, w) {
		return Settlement{}, ErrInvalidInput
	}
	if tx.Statement == nil {
		return Settlement{}, ErrInvalidInput
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return Settlement{}, ErrInvalidInput
	}
	if tx.Error != nil {
		return Settlement{}, settlementError(tx.Error)
	}
	out, err := applySettlement(tx, p, c, w)
	if err != nil {
		return Settlement{}, settlementError(err)
	}
	return out, nil
}

var errSettlement = errors.New("runtime settlement failed")

func settlementError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrDeleted):
		return ErrDeleted
	case errors.Is(err, ErrConflict), errors.Is(err, errSerialization):
		return ErrConflict
	default:
		return errSettlement
	}
}

func validSettlement(p, c string, w SettlementWrite) bool {
	if !validIDs(p, c, w.TurnID) || w.Checkpoint.Version > math.MaxInt64 || (w.Mode != CompleteTurn && w.Mode != HoldTurn) {
		return false
	}
	for _, e := range w.Events {
		if !validIDs(e.Kind) || (e.TurnID != "" && e.TurnID != w.TurnID) {
			return false
		}
	}
	return true
}

// Shared mutation body retains legacy Commit's transaction/retry policy outside.
func applySettlement(tx *gorm.DB, p, c string, w SettlementWrite) (Settlement, error) {
	r, err := load(tx, p, c)
	if err != nil {
		return Settlement{}, err
	}
	if r.Revision != w.ExpectedRevision || r.RunningTurn != w.TurnID {
		return Settlement{}, ErrConflict
	}
	// Check capacity before arithmetic or SQL: both supported dialects use
	// signed 64-bit integer storage. Exhaustion is a state conflict.
	if r.Revision >= math.MaxInt64 || r.LastSequence > math.MaxInt64 || uint64(len(w.Events)) > math.MaxInt64-r.LastSequence {
		return Settlement{}, ErrConflict
	}
	// Both modes require a real, unsettled active input, not just the row fence.
	var active inputRow
	if err = scope(tx, p, c).Where("input_id = ? AND settled = ?", w.TurnID, false).Take(&active).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Settlement{}, ErrConflict
		}
		return Settlement{}, err
	}
	var next inputRow
	running := w.TurnID
	if w.Mode == CompleteTurn {
		running = ""
		err = scope(tx, p, c).Where("settled = ? AND input_id <> ?", false, w.TurnID).Order("ordinal ASC").Take(&next).Error

		if err == nil {
			running = next.InputID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return Settlement{}, err
		}
	}
	err = cas(tx, r, map[string]any{
		"revision": r.Revision + 1, "running_turn": running, "last_sequence": r.LastSequence + uint64(len(w.Events)),
		"checkpoint_version": w.Checkpoint.Version, "checkpoint_context": w.Checkpoint.Context, "checkpoint_artifacts": w.Checkpoint.Artifacts, "checkpoint_pruning": w.Checkpoint.Pruning, "checkpoint_transcript_cursor": w.Checkpoint.TranscriptCursor,
	})
	if err != nil {
		return Settlement{}, err
	}
	if w.Mode == CompleteTurn {
		result := scope(tx.Model(&inputRow{}), p, c).Where("input_id = ? AND settled = ?", w.TurnID, false).Update("settled", true)
		if result.Error != nil {
			return Settlement{}, result.Error
		}
		if result.RowsAffected != 1 {
			return Settlement{}, ErrConflict
		}
	}
	for i, e := range w.Events {
		row := eventRow{Principal: p, Conversation: c, Sequence: r.LastSequence + uint64(i) + 1, TurnID: w.TurnID, Kind: e.Kind, Payload: e.Payload}
		if err = tx.Create(&row).Error; err != nil {
			return Settlement{}, err
		}
	}
	out := Settlement{Revision: r.Revision + 1}
	if w.Mode == CompleteTurn && running != "" {
		in := next.input()
		out.Next = &in
	}
	return out, nil
}
