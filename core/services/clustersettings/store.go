// Package clustersettings keeps the settings that every frontend of a
// distributed cluster must share: today the memory settings (model limit,
// eviction, model swap, watchdog, memory reclaimer and VRAM budget).
//
// Distributed-state mode: shared. A syncstate.SyncedMap named
// "settings.memory" replicates changes over the bus, and a DB-backed Store is
// the source of truth that survives a restart of the whole cluster. A write
// is a compare-and-swap on a revision, so two frontends never lose each
// other's partial update, and a client that read an old revision gets
// ErrStaleRevision.
package clustersettings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/syncstate"
)

// ErrStaleRevision means the caller changed a settings document from a
// revision that is no longer the current one. The HTTP layer answers 409.
var ErrStaleRevision = errors.New("the settings changed since they were read")

// errConflict is a lost compare-and-swap: another writer stored a revision
// first. The service reads again and retries.
var errConflict = errors.New("settings revision conflict")

// Entry is one settings document: a JSON object under a key, with the
// revision it was written at. It is the row of the table and the value of
// the synced map.
type Entry struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	Revision  int64     `gorm:"not null" json:"revision"`
	UpdatedBy string    `gorm:"size:255" json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName keeps the table apart from cluster_settings (the key/value
// settings of core/services/cluster), which has no revision.
func (Entry) TableName() string { return "cluster_shared_settings" }

var _ syncstate.Store[string, Entry] = (*DBStore)(nil)

// DBStore is the durable Store of the shared settings.
type DBStore struct{ db *gorm.DB }

// NewDBStore migrates the table under the schema-migrate advisory lock so
// frontends that start together do not race on it.
func NewDBStore(db *gorm.DB) (*DBStore, error) {
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&Entry{})
	}); err != nil {
		return nil, fmt.Errorf("clustersettings: migrating table: %w", err)
	}
	return &DBStore{db: db}, nil
}

// List returns every document.
func (s *DBStore) List(ctx context.Context) ([]Entry, error) {
	var out []Entry
	if err := s.db.WithContext(ctx).Find(&out).Error; err != nil {
		return nil, fmt.Errorf("clustersettings: reading: %w", err)
	}
	return out, nil
}

// Get returns one document, and false when it was never stored.
func (s *DBStore) Get(ctx context.Context, key string) (Entry, bool, error) {
	var e Entry
	err := s.db.WithContext(ctx).Where("key = ?", key).Take(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("clustersettings: reading %s: %w", key, err)
	}
	return e, true, nil
}

// Upsert stores v only when the stored revision is v.Revision-1 (or there
// is no row and v.Revision is 1). Otherwise it returns errConflict.
func (s *DBStore) Upsert(ctx context.Context, v Entry) error {
	db := s.db.WithContext(ctx)
	if v.Revision <= 1 {
		res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&v)
		if res.Error != nil {
			return fmt.Errorf("clustersettings: storing %s: %w", v.Key, res.Error)
		}
		if res.RowsAffected != 1 {
			return errConflict
		}
		return nil
	}
	res := db.Model(&Entry{}).
		Where("key = ? AND revision = ?", v.Key, v.Revision-1).
		Updates(map[string]any{"value": v.Value, "revision": v.Revision, "updated_by": v.UpdatedBy, "updated_at": v.UpdatedAt})
	if res.Error != nil {
		return fmt.Errorf("clustersettings: storing %s: %w", v.Key, res.Error)
	}
	if res.RowsAffected != 1 {
		return errConflict
	}
	return nil
}

// Delete removes a document.
func (s *DBStore) Delete(ctx context.Context, key string) error {
	if err := s.db.WithContext(ctx).Where("key = ?", key).Delete(&Entry{}).Error; err != nil {
		return fmt.Errorf("clustersettings: deleting %s: %w", key, err)
	}
	return nil
}
