package promptcachesvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/services/advisorylock"
)

// DefaultPurgeRetention is how long a purge stays in the purge log. Every
// worker that is online applies a purge within seconds; the log is for the
// workers that are offline. A worker whose cursor is older than a purge that
// the log dropped gets a purge of everything instead (see Ledger.Prune), so
// the retention bounds the size of the table, never what a worker misses. It
// is four times the default time to live of an entry, so a worker that was
// off for a few days applies the exact purges and keeps the rest.
const DefaultPurgeRetention = 30 * 24 * time.Hour

// Tombstone is one purge of the purge log.
type Tombstone struct {
	Seq       int64     `gorm:"primaryKey;autoIncrement"`
	All       bool      `gorm:"not null;default:false"`
	Model16   string    `gorm:"size:16"`
	NSIDs     string    `gorm:"type:text"` // comma separated
	CreatedAt time.Time `gorm:"not null;index"`
}

// TableName names the purge log.
func (Tombstone) TableName() string { return "prompt_cache_purges" }

// Scope is the purge of the tombstone.
func (t Tombstone) Scope() Scope {
	s := Scope{All: t.All, Model16: t.Model16}
	if t.NSIDs != "" {
		s.NSIDs = strings.Split(t.NSIDs, ",")
	}
	return s
}

// NodeSync is what one worker was last brought up to date with.
type NodeSync struct {
	NodeID string `gorm:"primaryKey;size:36"`
	// Incarnation is the worker process that answered. A worker with another
	// incarnation lost the settings it was sent.
	Incarnation     string `gorm:"size:64"`
	SettingsVersion string `gorm:"size:64"`
	// PurgedThrough is the last purge of the log the worker applied.
	PurgedThrough int64 `gorm:"not null;default:0"`
	// FullPurgeAt, when set, is a purge of everything last used before it,
	// owed to the worker because the log dropped purges it had not applied.
	FullPurgeAt *time.Time
	UpdatedAt   time.Time
}

// TableName names the table of the worker cursors.
func (NodeSync) TableName() string { return "prompt_cache_node_sync" }

// Ledger keeps the purge log and the cursor of each worker. It is the one
// store of the cluster: every frontend reads and writes the same rows, so a
// purge asked through one frontend reaches the workers through any other.
type Ledger interface {
	AddPurge(ctx context.Context, s Scope, at time.Time) (int64, error)
	// PurgesAfter lists up to limit purges with a sequence above seq, oldest
	// first.
	PurgesAfter(ctx context.Context, seq int64, limit int) ([]Tombstone, error)
	LastSeq(ctx context.Context) (int64, error)
	Nodes(ctx context.Context) (map[string]NodeSync, error)
	// EnsureNodes adds a cursor at the current end of nothing (0) for each
	// worker that has none, so a later Prune can tell what it missed.
	EnsureNodes(ctx context.Context, ids []string) error
	// RecordSync stores what a worker answered. The cursor only moves
	// forward, so two frontends that sync one worker at once never move it
	// back. A full purge owed up to fullPurgeDone is cleared.
	RecordSync(ctx context.Context, s NodeSync, fullPurgeDone *time.Time) error
	// Prune drops the purges older than before. A worker whose cursor is
	// behind a dropped purge is owed a purge of everything last used before
	// the newest dropped one, and its cursor moves past them.
	Prune(ctx context.Context, before time.Time) (int, error)
	// ForgetNodes drops the cursors of the workers not in keep.
	ForgetNodes(ctx context.Context, keep []string) error
}

// DBLedger is the Ledger on the database of the cluster.
type DBLedger struct{ db *gorm.DB }

var _ Ledger = (*DBLedger)(nil)

// NewDBLedger migrates the tables under the schema-migrate advisory lock, so
// frontends that start together do not race on them.
func NewDBLedger(db *gorm.DB) (*DBLedger, error) {
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&Tombstone{}, &NodeSync{})
	}); err != nil {
		return nil, fmt.Errorf("promptcachesvc: migrating the purge log: %w", err)
	}
	return &DBLedger{db: db}, nil
}

// AddPurge implements Ledger.
func (l *DBLedger) AddPurge(ctx context.Context, s Scope, at time.Time) (int64, error) {
	t := Tombstone{All: s.All, Model16: s.Model16, NSIDs: strings.Join(s.NSIDs, ","), CreatedAt: at.UTC()}
	if !t.All && t.Model16 == "" && t.NSIDs == "" {
		return 0, ErrNoScope
	}
	if err := l.db.WithContext(ctx).Create(&t).Error; err != nil {
		return 0, fmt.Errorf("promptcachesvc: storing a purge: %w", err)
	}
	return t.Seq, nil
}

// PurgesAfter implements Ledger.
func (l *DBLedger) PurgesAfter(ctx context.Context, seq int64, limit int) ([]Tombstone, error) {
	var out []Tombstone
	err := l.db.WithContext(ctx).Where("seq > ?", seq).Order("seq ASC").Limit(limit).Find(&out).Error
	return out, err
}

// LastSeq implements Ledger.
func (l *DBLedger) LastSeq(ctx context.Context) (int64, error) {
	var seq *int64
	err := l.db.WithContext(ctx).Model(&Tombstone{}).Select("MAX(seq)").Scan(&seq).Error
	if err != nil || seq == nil {
		return 0, err
	}
	return *seq, nil
}

// Nodes implements Ledger.
func (l *DBLedger) Nodes(ctx context.Context) (map[string]NodeSync, error) {
	var rows []NodeSync
	if err := l.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]NodeSync, len(rows))
	for _, r := range rows {
		out[r.NodeID] = r
	}
	return out, nil
}

// EnsureNodes implements Ledger.
func (l *DBLedger) EnsureNodes(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]NodeSync, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, NodeSync{NodeID: id, UpdatedAt: time.Now().UTC()})
	}
	return l.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// RecordSync implements Ledger.
func (l *DBLedger) RecordSync(ctx context.Context, s NodeSync, fullPurgeDone *time.Time) error {
	return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&NodeSync{NodeID: s.NodeID, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&NodeSync{}).Where("node_id = ?", s.NodeID).
			Updates(map[string]any{"incarnation": s.Incarnation, "settings_version": s.SettingsVersion, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&NodeSync{}).Where("node_id = ? AND purged_through < ?", s.NodeID, s.PurgedThrough).
			Update("purged_through", s.PurgedThrough).Error; err != nil {
			return err
		}
		if fullPurgeDone != nil {
			return tx.Model(&NodeSync{}).Where("node_id = ? AND full_purge_at <= ?", s.NodeID, fullPurgeDone.UTC()).
				Update("full_purge_at", nil).Error
		}
		return nil
	})
}

// Prune implements Ledger.
func (l *DBLedger) Prune(ctx context.Context, before time.Time) (int, error) {
	var dropped int
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var last Tombstone
		err := tx.Where("created_at < ?", before.UTC()).Order("seq DESC").Take(&last).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var newestRow Tombstone
		if err := tx.Where("seq <= ?", last.Seq).Order("created_at DESC").Take(&newestRow).Error; err != nil {
			return err
		}
		newest := newestRow.CreatedAt
		if err := tx.Model(&NodeSync{}).
			Where("purged_through < ? AND (full_purge_at IS NULL OR full_purge_at < ?)", last.Seq, newest).
			Update("full_purge_at", newest).Error; err != nil {
			return err
		}
		if err := tx.Model(&NodeSync{}).Where("purged_through < ?", last.Seq).
			Update("purged_through", last.Seq).Error; err != nil {
			return err
		}
		res := tx.Where("seq <= ?", last.Seq).Delete(&Tombstone{})
		dropped = int(res.RowsAffected)
		return res.Error
	})
	return dropped, err
}

// ForgetNodes implements Ledger. An empty keep is ignored: a registry that
// answers no node is more likely a failed read than a cluster with none.
func (l *DBLedger) ForgetNodes(ctx context.Context, keep []string) error {
	if len(keep) == 0 {
		return nil
	}
	return l.db.WithContext(ctx).Where("node_id NOT IN ?", keep).Delete(&NodeSync{}).Error
}
