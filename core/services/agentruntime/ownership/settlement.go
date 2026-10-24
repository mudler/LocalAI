// SPDX-License-Identifier: MIT
package ownership

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"gorm.io/gorm"
)

var errSettlement = errors.New("ownership: settlement failed")

// Settle fences and mutates in one transaction, deriving scope ONLY from l.Key.
// now is trusted host UTC time at the fence point, not a promise of lease
// validity throughout an arbitrarily long transaction or synchronized ACLs.
// No retries, revision rebasing, runner permissions or production wiring occur.
func (m *Manager) Settle(ctx context.Context, l Lease, now time.Time, w store.SettlementWrite) (store.Settlement, error) {
	var candidate store.Settlement
	err := m.fenced(ctx, l, now, func(tx *gorm.DB) error {
		var err error
		candidate, err = store.ApplySettlement(tx, l.Key.Principal, l.Key.Conversation, w)
		return err
	})
	if err != nil {
		// database/sql may finish automatic rollback before COMMIT, returning
		// ErrTxDone instead of the context error. Only normalize cancellation
		// outcomes of a failed transaction; never mask other failures or a
		// successfully committed candidate if the context changes afterward.
		if ctxErr := ctx.Err(); ctxErr != nil && (errors.Is(err, sql.ErrTxDone) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return store.Settlement{}, ctxErr
		}
		switch {
		case errors.Is(err, context.Canceled):
			err = context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			err = context.DeadlineExceeded
		case errors.Is(err, ErrLeaseLost):
			err = ErrLeaseLost
		case errors.Is(err, ErrInvalidInput):
			err = ErrInvalidInput
		case errors.Is(err, store.ErrInvalidInput):
			err = store.ErrInvalidInput
		case errors.Is(err, store.ErrConflict):
			err = store.ErrConflict
		case errors.Is(err, store.ErrNotFound):
			err = store.ErrNotFound
		case errors.Is(err, store.ErrDeleted):
			err = store.ErrDeleted
		default:
			err = errSettlement
		}
		return store.Settlement{}, err
	}
	return candidate, nil
}
