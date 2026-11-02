// SPDX-License-Identifier: MIT
package dberr

import (
	"context"

	"gorm.io/gorm"
)

type readTransactionKey struct{}
type readTransaction struct {
	pool gorm.ConnPool
	tx   *gorm.DB
}

// WithReadTransaction lets synchronous authorization reads reuse the transaction
// already holding the chat lock. The context must not escape its callback.
func WithReadTransaction(ctx context.Context, db, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, readTransactionKey{}, readTransaction{db.Statement.ConnPool, tx})
}

// ReadDB only reuses a transaction for the same pool; unrelated databases retain
// their own connection and all caller-supplied principal predicates.
func ReadDB(ctx context.Context, db *gorm.DB) *gorm.DB {
	if r, ok := ctx.Value(readTransactionKey{}).(readTransaction); ok && r.pool == db.Statement.ConnPool {
		return r.tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}
