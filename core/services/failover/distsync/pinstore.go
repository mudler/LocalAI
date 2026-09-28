// Package distsync wires failover.StateSync to syncstate.SyncedMap so target
// health, chain decisions and pins are shared across frontends over NATS,
// with pins durable in a small gorm-backed table.
package distsync

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"gorm.io/gorm"
)

// PinRecord is the durable form of a chain pin. It doubles as the value type
// of the "failover.pins" SyncedMap, so a hydrate (Store.List) needs no
// conversion and the wire delta carries the exact row.
type PinRecord struct {
	Chain     string    `gorm:"primaryKey" json:"chain"`
	Target    string    `json:"target"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName pins the table name independent of the Go type name.
func (PinRecord) TableName() string { return "failover_pins" }

// PinStore is gorm-backed durable storage for chain pins, implementing
// syncstate.Store[string, PinRecord] (asserted in distsync.go).
type PinStore struct {
	db *gorm.DB
}

// NewPinStore migrates the failover_pins table under the schema-migrate
// advisory lock - the same guard jobs.NewJobStore uses - so several
// frontends starting at once do not race on the migration, and returns a
// ready-to-use store.
func NewPinStore(db *gorm.DB) (*PinStore, error) {
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&PinRecord{})
	}); err != nil {
		return nil, fmt.Errorf("distsync: migrating pin table: %w", err)
	}
	return &PinStore{db: db}, nil
}

// List returns every durable pin, for hydrate on Start.
func (s *PinStore) List(ctx context.Context) ([]PinRecord, error) {
	var out []PinRecord
	if err := s.db.WithContext(ctx).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Upsert writes a pin through. Save inserts or updates by primary key.
func (s *PinStore) Upsert(ctx context.Context, v PinRecord) error {
	return s.db.WithContext(ctx).Save(&v).Error
}

// Delete removes a pin by chain name.
func (s *PinStore) Delete(ctx context.Context, k string) error {
	return s.db.WithContext(ctx).Delete(&PinRecord{Chain: k}).Error
}
