package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"
)

type notificationRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Principal string `gorm:"index:idx_studio_notifications_user,priority:1;uniqueIndex:idx_studio_notifications_key,priority:1;size:255;not null"`
	Kind      string `gorm:"size:32"`
	Status    string `gorm:"size:16"`
	Source    string `gorm:"size:16"`
	Title     string
	Body      string
	Link      string `gorm:"size:512;index:idx_studio_notifications_link,priority:2"`
	IsRead    bool   `gorm:"index:idx_studio_notifications_user,priority:2"`
	Created   time.Time
	// Code and Params let the UI word an item itself. Params is JSON text.
	Code   string `gorm:"size:64"`
	Params string
	// DedupeKey is NULL for a plain row. A principal has one row for a key; the
	// unique index treats NULLs as distinct in SQLite and PostgreSQL alike.
	DedupeKey *string `gorm:"size:96;uniqueIndex:idx_studio_notifications_key,priority:2"`
	// Payload is the JSON list of ids of a set row, empty for any other row.
	Payload string
	Hidden  bool `gorm:"not null;default:false"`
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

// notSticky selects the rows that a trim may remove: every row but the sets.
const notSticky = "(payload IS NULL OR payload = '')"

func (r notificationRow) toNotification() Notification {
	n := Notification{ID: r.ID, Principal: r.Principal, Kind: r.Kind, Status: r.Status, Source: r.Source, Title: r.Title,
		Body: r.Body, Link: r.Link, Read: r.IsRead, Created: r.Created, Code: r.Code, Params: decodeParams(r.Params),
		Ids: decodeIds(r.Payload), Hidden: r.Hidden}
	if r.DedupeKey != nil {
		n.Key = *r.DedupeKey
	}
	return n
}

func newRow(n Notification) (notificationRow, error) {
	params, err := encodeParams(n.Params)
	if err != nil {
		return notificationRow{}, err
	}
	row := notificationRow{Principal: n.Principal, Kind: n.Kind, Status: n.Status, Source: n.Source, Title: n.Title, Body: n.Body,
		Link: n.Link, Created: n.Created, Code: n.Code, Params: params, Payload: encodeIds(n.Ids)}
	if n.Key != "" {
		k := n.Key
		row.DedupeKey = &k
	}
	return row, nil
}

// trim removes the rows of the principal that are past MaxAge counted back from
// at, then the rows past MaxPerUser. Set rows stay.
func trim(tx *gorm.DB, principal string, at time.Time) error {
	if err := tx.Where("principal = ? AND created < ? AND "+notSticky, principal, at.Add(-MaxAge)).Delete(&notificationRow{}).Error; err != nil {
		return err
	}
	// The MaxPerUser-th newest id is the oldest one that stays.
	var edge []int64
	if err := tx.Model(&notificationRow{}).Where("principal = ? AND "+notSticky, principal).Order("id DESC").
		Offset(MaxPerUser-1).Limit(1).Pluck("id", &edge).Error; err != nil {
		return err
	}
	if len(edge) == 1 {
		return tx.Where("principal = ? AND id < ? AND "+notSticky, principal, edge[0]).Delete(&notificationRow{}).Error
	}
	return nil
}

func (s *gormStore) Add(ctx context.Context, n Notification) (int64, error) {
	n.Key, n.Ids = "", nil
	row, err := newRow(n)
	if err != nil {
		return 0, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return trim(tx, n.Principal, n.Created)
	})
	return row.ID, err
}

func (s *gormStore) Upsert(ctx context.Context, n Notification, mode UpsertMode) (Notification, UpsertResult, error) {
	if n.Key == "" {
		return Notification{}, "", errors.New("notify: upsert needs a key")
	}
	if mode == ModeOnce {
		n.Ids = nil
	}
	var (
		out Notification
		res UpsertResult
	)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var found []notificationRow
		if err := tx.Where("principal = ? AND dedupe_key = ?", n.Principal, n.Key).Limit(1).Find(&found).Error; err != nil {
			return err
		}
		var old *Notification
		if len(found) == 1 {
			o := found[0].toNotification()
			old = &o
		}
		res = Decide(old, n, mode)
		switch res {
		case ResultUnchanged:
			if old != nil {
				out = *old
			}
			return nil
		case ResultRemoved:
			out = *old
			return tx.Where("id = ?", found[0].ID).Delete(&notificationRow{}).Error
		case ResultUpdated:
			params, err := encodeParams(n.Params)
			if err != nil {
				return err
			}
			if err := tx.Model(&notificationRow{}).Where("id = ?", found[0].ID).Updates(map[string]any{
				"kind": n.Kind, "status": n.Status, "source": n.Source, "title": n.Title, "body": n.Body, "link": n.Link,
				"code": n.Code, "params": params, "payload": encodeIds(n.Ids)}).Error; err != nil {
				return err
			}
			out = *old
			out.Kind, out.Status, out.Source, out.Title, out.Body, out.Link = n.Kind, n.Status, n.Source, n.Title, n.Body, n.Link
			out.Code, out.Params, out.Ids = n.Code, n.Params, slices.Clone(n.Ids)
			return nil
		}
		// Created and revived store a new row, so a revived item sorts first and
		// clients that watch for ids above the highest one seen announce it.
		if res == ResultRevived {
			if err := tx.Where("id = ?", found[0].ID).Delete(&notificationRow{}).Error; err != nil {
				return err
			}
		}
		row, err := newRow(n)
		if err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		out = row.toNotification()
		return trim(tx, n.Principal, n.Created)
	})
	if err != nil {
		return Notification{}, "", err
	}
	return out, res, nil
}

func (s *gormStore) List(ctx context.Context, principal string, unreadOnly bool, limit int) ([]Notification, error) {
	q := s.db.WithContext(ctx).Where("principal = ? AND hidden = ?", principal, false).Order("id DESC")
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
		out[i] = r.toNotification()
	}
	return out, nil
}

func (s *gormStore) Count(ctx context.Context, principal string) (int, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&notificationRow{}).Where("principal = ? AND is_read = ? AND hidden = ?", principal, false, false).Count(&n).Error
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

// remove deletes the plain rows of the principal that match and hides the
// keyed ones, which keep their key and ids so an emitter can tell "dismissed"
// from "never posted". ids limits it to those rows; readOnly to the read ones.
// It returns how many visible rows went.
func (s *gormStore) remove(ctx context.Context, principal string, ids []int64, onlyIDs, readOnly bool) (int, error) {
	scope := func(q *gorm.DB) *gorm.DB {
		q = q.Where("principal = ?", principal)
		if onlyIDs {
			q = q.Where("id IN ?", ids)
		}
		if readOnly {
			q = q.Where("is_read = ?", true)
		}
		return q
	}
	n := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := scope(tx.Where("dedupe_key IS NULL")).Delete(&notificationRow{})
		if res.Error != nil {
			return res.Error
		}
		n += int(res.RowsAffected)
		hid := scope(tx.Model(&notificationRow{}).Where("dedupe_key IS NOT NULL AND hidden = ?", false)).
			Updates(map[string]any{"hidden": true, "is_read": true})
		if hid.Error != nil {
			return hid.Error
		}
		n += int(hid.RowsAffected)
		return nil
	})
	return n, err
}

func (s *gormStore) Delete(ctx context.Context, principal string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.remove(ctx, principal, ids, true, false)
	return err
}

func (s *gormStore) DeleteAll(ctx context.Context, principal string, readOnly bool) (int, error) {
	return s.remove(ctx, principal, nil, false, readOnly)
}
