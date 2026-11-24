// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"gorm.io/gorm"
)

// GormStore keeps the pins in the account database, which every frontend of a
// deployment shares. No process holds the only copy.
type GormStore struct{ db *gorm.DB }

func NewGormStore(db *gorm.DB) (*GormStore, error) {
	// Several frontends migrate the shared database at startup: take the
	// schema-migrate lock like every other shared table does.
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&Pin{})
	}); err != nil {
		return nil, fmt.Errorf("migrating addon pin tables: %w", err)
	}
	return &GormStore{db: db}, nil
}

func (s *GormStore) List(ctx context.Context, userID string) ([]Pin, error) {
	var out []Pin
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("position asc, addon_id asc").Find(&out).Error
	return out, err
}

func (s *GormStore) Replace(ctx context.Context, userID string, ids []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old []Pin
		if err := tx.Where("user_id = ?", userID).Find(&old).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&Pin{}).Error; err != nil {
			return err
		}
		rows := rebuild(old, userID, ids)
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

func (s *GormStore) Distinct(ctx context.Context) ([]string, error) {
	var out []string
	err := s.db.WithContext(ctx).Model(&Pin{}).Distinct().Pluck("addon_id", &out).Error
	return out, err
}

func (s *GormStore) MarkMissing(ctx context.Context, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&Pin{}).Where("addon_id IN ? AND missing_since IS NULL", ids).Update("missing_since", now).Error
}

func (s *GormStore) ClearMissing(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&Pin{}).Where("addon_id IN ?", ids).Update("missing_since", gorm.Expr("NULL")).Error
}

func (s *GormStore) DeleteMissingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("missing_since IS NOT NULL AND missing_since < ?", cutoff).Delete(&Pin{})
	return res.RowsAffected, res.Error
}
