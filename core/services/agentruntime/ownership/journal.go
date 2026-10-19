// SPDX-License-Identifier: MIT
package ownership

import (
	"bytes"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

func record(r toolRow) ToolRecord {
	return ToolRecord{Call: ToolCall{r.TurnID, r.RequestID, r.ArgumentsHash, uint64(r.Attempt)}, Status: r.Status, Result: bytes.Clone(r.Result), ResolutionID: r.ResolutionID}
}
func sameCall(r toolRow, c ToolCall) bool {
	return r.ArgumentsHash == c.ArgumentsHash && r.Attempt == int64(c.Attempt)
}
func saveTool(tx *gorm.DB, r *toolRow) error { return tx.Save(r).Error }
func (m *Manager) Prepare(ctx context.Context, l Lease, n time.Time, c ToolCall) (ToolRecord, error) {
	if !validCall(c) {
		return ToolRecord{}, ErrInvalidInput
	}
	var out ToolRecord
	e := m.fenced(ctx, l, n, func(tx *gorm.DB) error {
		r, e := lookup(tx, l.Key, c)
		if errors.Is(e, ErrNotFound) {
			r = toolRow{Principal: l.Principal, Conversation: l.Conversation, TurnID: c.TurnID, RequestID: c.RequestID, ArgumentsHash: c.ArgumentsHash, Attempt: int64(c.Attempt), Status: Prepared}
			if e = tx.Create(&r).Error; e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else if !sameCall(r, c) {
			return ErrConflict
		}
		out = record(r)
		return nil
	})
	if e != nil {
		return ToolRecord{}, e
	}
	return out, nil
}

func (m *Manager) change(ctx context.Context, l Lease, n time.Time, c ToolCall, fn func(*toolRow) error) (ToolRecord, error) {
	if !validCall(c) {
		return ToolRecord{}, ErrInvalidInput
	}
	var out ToolRecord
	e := m.fenced(ctx, l, n, func(tx *gorm.DB) error {
		r, e := lookup(tx, l.Key, c)
		if e != nil {
			return e
		}
		if !sameCall(r, c) {
			return ErrConflict
		}
		if e = fn(&r); e != nil {
			return e
		}
		if e = saveTool(tx, &r); e != nil {
			return e
		}
		out = record(r)
		return nil
	})
	if e != nil {
		return ToolRecord{}, e
	}
	return out, nil
}

// Dispatch returns permission to execute only on the Prepared -> Dispatched
// transition. Every subsequent call conflicts, including completed requests.
func (m *Manager) Dispatch(ctx context.Context, l Lease, n time.Time, c ToolCall) (ToolRecord, error) {
	return m.change(ctx, l, n, c, func(r *toolRow) error {
		if r.Status != Prepared {
			return ErrConflict
		}
		r.Status = Dispatched
		r.DispatchEpoch = int64(l.Epoch)
		return nil
	})
}

// Complete accepts nil as empty result data. Identical completed callbacks are
// idempotent; a callback can never alter a reconciled or differently bound result.
func (m *Manager) Complete(ctx context.Context, l Lease, n time.Time, c ToolCall, result []byte) (ToolRecord, error) {
	result = bytes.Clone(result)
	return m.change(ctx, l, n, c, func(r *toolRow) error {
		if r.Status == Completed {
			if r.ResolutionID == "" && bytes.Equal(r.Result, result) {
				return nil
			}
			return ErrConflict
		}
		if r.Status != Dispatched || r.DispatchEpoch != int64(l.Epoch) {
			return ErrConflict
		}
		r.Status = Completed
		r.Result = result
		return nil
	})
}

// Reconcile admits a host-verified outcome, not an execution instruction. The
// caller must authenticate the evidence outside this package. Nil means empty.
func (m *Manager) Reconcile(ctx context.Context, l Lease, n time.Time, c ToolCall, id string, result []byte) (ToolRecord, error) {
	if !validID(id) {
		return ToolRecord{}, ErrInvalidInput
	}
	result = bytes.Clone(result)
	return m.change(ctx, l, n, c, func(r *toolRow) error {
		if r.Status == Completed {
			if r.ResolutionID == id && bytes.Equal(r.Result, result) {
				return nil
			}
			return ErrConflict
		}
		if r.Status != Unknown {
			return ErrConflict
		}
		r.Status = Completed
		r.Result = result
		r.ResolutionID = id
		return nil
	})
}
func (m *Manager) list(ctx context.Context, l Lease, n time.Time, turn string, recover bool) ([]ToolRecord, error) {
	if !validID(turn) {
		return nil, ErrInvalidInput
	}
	var out []ToolRecord
	e := m.fenced(ctx, l, n, func(tx *gorm.DB) error {
		q := scope(tx, l.Key).Where("turn_id = ?", turn)
		if recover {
			if e := q.Model(&toolRow{}).Where("status = ? AND dispatch_epoch < ?", Dispatched, int64(l.Epoch)).UpdateColumn("status", Unknown).Error; e != nil {
				return e
			}
		}
		var rows []toolRow
		if e := scope(tx, l.Key).Where("turn_id = ?", turn).Order("request_id ASC").Find(&rows).Error; e != nil {
			return e
		}
		out = make([]ToolRecord, 0, len(rows))
		for _, r := range rows {
			out = append(out, record(r))
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (m *Manager) Records(ctx context.Context, l Lease, n time.Time, t string) ([]ToolRecord, error) {
	return m.list(ctx, l, n, t, false)
}
func (m *Manager) Recover(ctx context.Context, l Lease, n time.Time, t string) ([]ToolRecord, error) {
	return m.list(ctx, l, n, t, true)
}
