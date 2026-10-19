// SPDX-License-Identifier: MIT
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Create is idempotent for live conversations; it never resurrects tombstones.
func (s *Store) Create(ctx context.Context, p, c string) error {
	if !validIDs(p, c) {
		return ErrInvalidInput
	}
	return s.transaction(ctx, func(tx *gorm.DB) error {
		r := conversationRow{Principal: p, Conversation: c}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error; err != nil {
			return err
		}
		_, err := load(tx, p, c)
		return err
	})
}

// Admit returns the original result for retries, even when that result's revision
// and RunningTurn are no longer current. Get supplies the current CAS fence.
func (s *Store) Admit(ctx context.Context, p, c, id string, payload []byte) (Admission, error) {
	if !validIDs(p, c, id) {
		return Admission{}, ErrInvalidInput
	}
	payload = append([]byte(nil), payload...)
	hash := sha256.Sum256(payload)
	var out Admission
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		r, err := load(tx, p, c)
		if err != nil {
			return err
		}
		var previous inputRow
		err = scope(tx, p, c).Where("input_id = ?", id).Take(&previous).Error
		if err == nil {
			if !bytes.Equal(previous.PayloadHash, hash[:]) || !bytes.Equal(previous.Payload, payload) {
				return ErrConflict
			}
			out = previous.admission()
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		running := r.RunningTurn
		queued := running != ""
		if !queued {
			running = id
		}
		if err = cas(tx, r, map[string]any{"revision": r.Revision + 1, "running_turn": running, "last_ordinal": r.LastOrdinal + 1}); err != nil {
			return err
		}
		in := inputRow{Principal: p, Conversation: c, InputID: id, Payload: payload, PayloadHash: hash[:], Ordinal: r.LastOrdinal + 1, AdmissionRunning: running, AdmissionQueued: queued, AdmissionRevision: r.Revision + 1}
		if err = tx.Create(&in).Error; err != nil {
			return err
		}
		out = in.admission()
		return nil
	})
	if err != nil {
		return Admission{}, err
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, p, c string) error {
	if !validIDs(p, c) {
		return ErrInvalidInput
	}
	return s.transaction(ctx, func(tx *gorm.DB) error {
		r, err := load(tx, p, c)
		if errors.Is(err, ErrDeleted) {
			return nil
		}
		if err != nil {
			return err
		}
		// Keep inputs/checkpoint/outbox and dedup authority. Task6 owns retention.
		return cas(tx, r, map[string]any{"revision": r.Revision + 1, "deleted": true})
	})
}
