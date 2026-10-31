package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type notificationRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Principal string `gorm:"index:idx_studio_notifications_user,priority:1;size:255;not null"`
	Kind      string `gorm:"size:32"`
	Status    string `gorm:"size:16"`
	Title     string
	Body      string
	Link      string `gorm:"size:512;index:idx_studio_notifications_link,priority:2"`
	IsRead    bool   `gorm:"index:idx_studio_notifications_user,priority:2"`
	Created   time.Time
}

func (notificationRow) TableName() string { return "studio_notifications" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its table.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("notify: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&notificationRow{}); err != nil {
		return nil, fmt.Errorf("notify: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (s *gormStore) Add(ctx context.Context, n Notification) (int64, error) {
	row := notificationRow{Principal: n.Principal, Kind: n.Kind, Status: n.Status, Title: n.Title, Body: n.Body, Link: n.Link, Created: n.Created}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("principal = ? AND created < ?", n.Principal, n.Created.Add(-MaxAge)).Delete(&notificationRow{}).Error; err != nil {
			return err
		}
		// The MaxPerUser-th newest id is the oldest one that stays.
		var edge []int64
		if err := tx.Model(&notificationRow{}).Where("principal = ?", n.Principal).Order("id DESC").
			Offset(MaxPerUser-1).Limit(1).Pluck("id", &edge).Error; err != nil {
			return err
		}
		if len(edge) == 1 {
			return tx.Where("principal = ? AND id < ?", n.Principal, edge[0]).Delete(&notificationRow{}).Error
		}
		return nil
	})
	return row.ID, err
}

func (s *gormStore) List(ctx context.Context, principal string, unreadOnly bool, limit int) ([]Notification, error) {
	q := s.db.WithContext(ctx).Where("principal = ?", principal).Order("id DESC")
	if unreadOnly {
		q = q.Where("is_read = ?", false)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []notificationRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Notification, len(rows))
	for i, r := range rows {
		out[i] = Notification{ID: r.ID, Principal: r.Principal, Kind: r.Kind, Status: r.Status, Title: r.Title,
			Body: r.Body, Link: r.Link, Read: r.IsRead, Created: r.Created}
	}
	return out, nil
}

func (s *gormStore) Count(ctx context.Context, principal string) (int, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&notificationRow{}).Where("principal = ? AND is_read = ?", principal, false).Count(&n).Error
	return int(n), err
}

func (s *gormStore) markRead(ctx context.Context, q *gorm.DB) error {
	return q.WithContext(ctx).Model(&notificationRow{}).Update("is_read", true).Error
}

func (s *gormStore) MarkRead(ctx context.Context, principal string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.markRead(ctx, s.db.Where("principal = ? AND id IN ?", principal, ids))
}

func (s *gormStore) MarkChatRead(ctx context.Context, principal, link string) error {
	return s.markRead(ctx, s.db.Where("principal = ? AND link = ?", principal, link))
}

func (s *gormStore) MarkAllRead(ctx context.Context, principal string) error {
	return s.markRead(ctx, s.db.Where("principal = ?", principal))
}

func (s *gormStore) Delete(ctx context.Context, principal string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Where("principal = ? AND id IN ?", principal, ids).Delete(&notificationRow{}).Error
}

func (s *gormStore) DeleteAll(ctx context.Context, principal string, readOnly bool) (int, error) {
	q := s.db.WithContext(ctx).Where("principal = ?", principal)
	if readOnly {
		q = q.Where("is_read = ?", true)
	}
	res := q.Delete(&notificationRow{})
	return int(res.RowsAffected), res.Error
}
