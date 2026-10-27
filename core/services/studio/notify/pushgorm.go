package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type pushRow struct {
	Endpoint  string `gorm:"primaryKey;size:1024"`
	Principal string `gorm:"index;size:255;not null"`
	P256dh    string `gorm:"size:255"`
	Auth      string `gorm:"size:255"`
	Created   time.Time
}

func (pushRow) TableName() string { return "studio_push_subscriptions" }

// vapidRow is the server's key pair: one row, named "vapid".
type vapidRow struct {
	Name    string `gorm:"primaryKey;size:32"`
	Private string
	Public  string
}

func (vapidRow) TableName() string { return "studio_push_keys" }

type gormPush struct{ db *gorm.DB }

// NewGORMPush returns a PushStore on db (SQLite or PostgreSQL) and creates its
// tables.
func NewGORMPush(db *gorm.DB) (PushStore, error) {
	if db == nil {
		return nil, errors.New("notify: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&pushRow{}, &vapidRow{}); err != nil {
		return nil, fmt.Errorf("notify: migrate: %w", err)
	}
	return &gormPush{db: db}, nil
}

func (s *gormPush) Subscribe(ctx context.Context, sub PushSubscription) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := pushRow{Endpoint: sub.Endpoint, Principal: sub.Principal, P256dh: sub.P256dh, Auth: sub.Auth, Created: sub.Created}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		var edge []time.Time
		if err := tx.Model(&pushRow{}).Where("principal = ?", sub.Principal).Order("created DESC, endpoint").
			Offset(MaxPushSubscriptions-1).Limit(1).Pluck("created", &edge).Error; err != nil {
			return err
		}
		if len(edge) == 1 {
			return tx.Where("principal = ? AND created < ?", sub.Principal, edge[0]).Delete(&pushRow{}).Error
		}
		return nil
	})
}

func (s *gormPush) Unsubscribe(ctx context.Context, principal, endpoint string) error {
	return s.db.WithContext(ctx).Where("principal = ? AND endpoint = ?", principal, endpoint).Delete(&pushRow{}).Error
}

func (s *gormPush) ForPrincipal(ctx context.Context, principal string) ([]PushSubscription, error) {
	var rows []pushRow
	if err := s.db.WithContext(ctx).Where("principal = ?", principal).Order("created, endpoint").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]PushSubscription, len(rows))
	for i, r := range rows {
		out[i] = PushSubscription{Principal: r.Principal, Endpoint: r.Endpoint, P256dh: r.P256dh, Auth: r.Auth, Created: r.Created}
	}
	return out, nil
}

func (s *gormPush) Drop(ctx context.Context, endpoint string) error {
	return s.db.WithContext(ctx).Where("endpoint = ?", endpoint).Delete(&pushRow{}).Error
}

func (s *gormPush) EnsureVAPID(ctx context.Context, generate func() (string, string, error)) (string, string, error) {
	read := func() (vapidRow, bool, error) {
		var r vapidRow
		err := s.db.WithContext(ctx).Where("name = ?", "vapid").Take(&r).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r, false, nil
		}
		return r, err == nil, err
	}
	if r, ok, err := read(); err != nil || ok {
		return r.Private, r.Public, err
	}
	priv, pub, err := generate()
	if err != nil {
		return "", "", err
	}
	// Two frontends may race: the first insert wins and both read it back.
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&vapidRow{Name: "vapid", Private: priv, Public: pub}).Error; err != nil {
		return "", "", err
	}
	r, ok, err := read()
	if err != nil || !ok {
		return "", "", fmt.Errorf("notify: the VAPID keys were not stored: %v", err)
	}
	return r.Private, r.Public, nil
}
