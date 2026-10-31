// SPDX-License-Identifier: MIT
package ownership

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Manager struct{ db *gorm.DB }
type leaseRow struct {
	Principal    string `gorm:"primaryKey;autoIncrement:false"`
	Conversation string `gorm:"primaryKey;autoIncrement:false"`
	Owner        string
	Epoch        int64
	Expires      int64
}

func (leaseRow) TableName() string { return "runtime_ownership_leases" }

type toolRow struct {
	Principal     string `gorm:"primaryKey;autoIncrement:false"`
	Conversation  string `gorm:"primaryKey;autoIncrement:false"`
	TurnID        string `gorm:"primaryKey;autoIncrement:false"`
	RequestID     string `gorm:"primaryKey;autoIncrement:false"`
	ArgumentsHash string
	Attempt       int64
	DispatchEpoch int64
	Status        ToolStatus
	Result        []byte
	ResolutionID  string
}

func (toolRow) TableName() string { return "runtime_ownership_tools" }
func New(db *gorm.DB) (*Manager, error) {
	if db == nil {
		return nil, ErrInvalidInput
	}
	if err := db.AutoMigrate(&leaseRow{}, &toolRow{}, &executionRow{}); err != nil {
		return nil, err
	}
	return &Manager{db: db}, nil
}
func validID(s string) bool { return len(s) <= 512 && strings.TrimSpace(s) != "" }
func validKey(k Key) bool   { return validID(k.Principal) && validID(k.Conversation) }
func validNow(n time.Time) bool {
	return n.Location() == time.UTC && !n.IsZero() && n.UnixNano() > 0 && time.Unix(0, n.UnixNano()).UTC().Equal(n)
}
func validTTL(n time.Time, t time.Duration) bool { return t > 0 && t <= MaxTTL && validNow(n.Add(t)) }
func validLease(l Lease, n time.Time) bool {
	return validKey(l.Key) && validID(l.Owner) && l.Epoch > 0 && l.Epoch <= math.MaxInt64 && validNow(n)
}
func validCall(c ToolCall) bool {
	return validID(c.TurnID) && validID(c.RequestID) && validID(c.ArgumentsHash) && c.Attempt > 0 && c.Attempt <= math.MaxInt64
}
func scope(tx *gorm.DB, k Key) *gorm.DB {
	return tx.Where("principal = ? AND conversation = ?", k.Principal, k.Conversation)
}
func asLease(r leaseRow) Lease {
	return Lease{Key: Key{r.Principal, r.Conversation}, Owner: r.Owner, Epoch: uint64(r.Epoch), ExpiresAt: time.Unix(0, r.Expires).UTC()}
}

func (m *Manager) Acquire(ctx context.Context, k Key, o string, n time.Time, t time.Duration) (Lease, error) {
	if !validKey(k) || !validID(o) || !validNow(n) || !validTTL(n, t) {
		return Lease{}, ErrInvalidInput
	}
	var out Lease
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Write first: serialize contenders before reading, including SQLite deferred
		// transactions. No process-local mutex or check-then-write authority gap.
		r := leaseRow{Principal: k.Principal, Conversation: k.Conversation}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error; e != nil {
			return e
		}
		if e := scope(tx, k).Model(&leaseRow{}).UpdateColumn("epoch", gorm.Expr("epoch")).Error; e != nil {
			return e
		}
		if e := scope(tx, k).First(&r).Error; e != nil {
			return e
		}
		if r.Owner != "" && r.Expires > n.UnixNano() {
			if r.Owner != o {
				return ErrConflict
			}
			out = asLease(r)
			return nil
		}
		if r.Epoch == math.MaxInt64 {
			return ErrInvalidInput
		}
		r.Owner = o
		r.Epoch++
		r.Expires = n.Add(t).UnixNano()
		if e := scope(tx, k).Model(&leaseRow{}).Updates(map[string]any{"owner": r.Owner, "epoch": r.Epoch, "expires": r.Expires}).Error; e != nil {
			return e
		}
		out = asLease(r)
		return nil
	})
	if err != nil {
		return Lease{}, err
	}
	return out, nil
}

// fenced acquires the database write lock and checks current authority in the
// same transaction as every journal read/write. ExpiresAt in a token is a hint;
// owner+epoch, not a stale client copy of the renewed expiry, defines authority.
func (m *Manager) fenced(ctx context.Context, l Lease, n time.Time, fn func(*gorm.DB) error) error {
	if !validLease(l, n) {
		return ErrInvalidInput
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := scope(tx, l.Key).Model(&leaseRow{}).Where("owner = ? AND epoch = ? AND expires > ?", l.Owner, int64(l.Epoch), n.UnixNano()).UpdateColumn("epoch", gorm.Expr("epoch"))
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return ErrLeaseLost
		}
		return fn(tx)
	})
}
func (m *Manager) Check(ctx context.Context, l Lease, n time.Time) error {
	return m.fenced(ctx, l, n, func(*gorm.DB) error { return nil })
}
func (m *Manager) Renew(ctx context.Context, l Lease, n time.Time, t time.Duration) (Lease, error) {
	if !validNow(n) || !validTTL(n, t) {
		return Lease{}, ErrInvalidInput
	}
	var out Lease
	e := m.fenced(ctx, l, n, func(tx *gorm.DB) error {
		var r leaseRow
		if e := scope(tx, l.Key).First(&r).Error; e != nil {
			return e
		}
		// Renewal cannot accidentally shorten a live grant.
		if end := n.Add(t).UnixNano(); end > r.Expires {
			r.Expires = end
		}
		if e := scope(tx, l.Key).Model(&leaseRow{}).UpdateColumn("expires", r.Expires).Error; e != nil {
			return e
		}
		out = asLease(r)
		return nil
	})
	if e != nil {
		return Lease{}, e
	}
	return out, nil
}
func (m *Manager) Release(ctx context.Context, l Lease, n time.Time) error {
	return m.fenced(ctx, l, n, func(tx *gorm.DB) error {
		return scope(tx, l.Key).Model(&leaseRow{}).Updates(map[string]any{"owner": "", "expires": int64(0)}).Error
	})
}
func lookup(tx *gorm.DB, k Key, c ToolCall) (toolRow, error) {
	var r toolRow
	e := scope(tx, k).Where("turn_id = ? AND request_id = ?", c.TurnID, c.RequestID).First(&r).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return r, ErrNotFound
	}
	return r, e
}
