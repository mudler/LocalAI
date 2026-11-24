// SPDX-License-Identifier: MIT
package usersettings

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GormStore keeps the answers in the account database, which every frontend of
// a deployment shares.
type GormStore struct{ db *gorm.DB }

func NewGormStore(db *gorm.DB) (*GormStore, error) {
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&Setting{})
	}); err != nil {
		return nil, fmt.Errorf("migrating add-on user setting tables: %w", err)
	}
	return &GormStore{db: db}, nil
}

func (s *GormStore) Get(ctx context.Context, userID, addonID string) (map[string]string, error) {
	var rows []Setting
	if err := s.db.WithContext(ctx).Where("user_id = ? AND addon_id = ?", userID, addonID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

func (s *GormStore) Set(ctx context.Context, userID, addonID, key, value string) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "addon_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&Setting{UserID: userID, AddonID: addonID, Key: key, Value: value}).Error
}
