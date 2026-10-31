// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

type conversationRow struct {
	Principal    string `gorm:"primaryKey;size:256;autoIncrement:false"`
	Conversation string `gorm:"primaryKey;size:256;autoIncrement:false"`
	Revision     uint64
	RunningTurn  string
	LastOrdinal  uint64
	LastSequence uint64
	Deleted      bool
	Checkpoint   Checkpoint `gorm:"embedded;embeddedPrefix:checkpoint_"`
}

func (conversationRow) TableName() string { return "runtime_conversations" }

type inputRow struct {
	Principal         string `gorm:"primaryKey;size:256;autoIncrement:false"`
	Conversation      string `gorm:"primaryKey;size:256;autoIncrement:false"`
	InputID           string `gorm:"primaryKey;size:256;autoIncrement:false"`
	Payload           []byte
	PayloadHash       []byte
	Ordinal           uint64
	Settled           bool
	AdmissionRunning  string
	AdmissionQueued   bool
	AdmissionRevision uint64
}

func (inputRow) TableName() string { return "runtime_inputs" }
func (r inputRow) input() Input {
	return Input{InputID: r.InputID, Payload: append([]byte(nil), r.Payload...), PayloadHash: append([]byte(nil), r.PayloadHash...), Ordinal: r.Ordinal}
}
func (r inputRow) admission() Admission {
	return Admission{InputID: r.InputID, RunningTurn: r.AdmissionRunning, Queued: r.AdmissionQueued, Revision: r.AdmissionRevision}
}

type eventRow struct {
	Principal    string `gorm:"primaryKey;size:256;autoIncrement:false"`
	Conversation string `gorm:"primaryKey;size:256;autoIncrement:false"`
	Sequence     uint64 `gorm:"primaryKey;autoIncrement:false"`
	TurnID       string
	Kind         string
	Payload      []byte
}

func (eventRow) TableName() string { return "runtime_events" }

type Store struct{ db *gorm.DB }

// New migrates only runtime-owned tables. The caller owns the DB and its pool.
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, ErrInvalidInput
	}
	if err := db.AutoMigrate(&conversationRow{}, &inputRow{}, &eventRow{}); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func validIDs(ids ...string) bool {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || len(id) > MaxIdentityBytes {
			return false
		}
	}
	return true
}
func scope(db *gorm.DB, p, c string) *gorm.DB {
	return db.Where("principal = ? AND conversation = ?", p, c)
}
func load(db *gorm.DB, p, c string) (conversationRow, error) {
	var r conversationRow
	err := scope(db, p, c).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if r.Deleted {
		return r, ErrDeleted
	}
	return r, nil
}

// A lost CAS is a local serialization conflict, not a public stale-commit
// conflict. Each retry rechecks state; Commit also checks its caller's fence.
var errSerialization = errors.New("runtime transaction lost CAS")

func retryable(err error) bool {
	if errors.Is(err, errSerialization) {
		return true
	}
	var se sqlite3.Error
	if errors.As(err, &se) {
		return se.Code == sqlite3.ErrBusy || se.Code == sqlite3.ErrLocked
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return state.SQLState() == "40001" || state.SQLState() == "40P01"
	}
	return false
}

// SQLite reads within a transaction share one database snapshot. Retrying replaces
// the entire transaction, including its reads, never just a failed statement.
func (s *Store) transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	const attempts = 64
	for i := 0; i < attempts; i++ {
		err := s.db.WithContext(ctx).Transaction(fn)
		if err == nil || !retryable(err) || i == attempts-1 {
			return err
		}
		delay := time.Duration(1+min(i, 20)) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable")
}

func cas(tx *gorm.DB, r conversationRow, values map[string]any) error {
	result := scope(tx.Model(&conversationRow{}), r.Principal, r.Conversation).Where("revision = ? AND deleted = ?", r.Revision, false).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errSerialization
	}
	return nil
}
