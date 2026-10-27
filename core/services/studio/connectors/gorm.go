package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type bindingRow struct {
	ID         string `gorm:"primaryKey;size:64"`
	Owner      string `gorm:"index;size:255;not null"`
	Kind       string `gorm:"size:32"`
	AddonID    string `gorm:"size:64"`
	Name       string `gorm:"size:255"`
	AgentID    string `gorm:"size:128"`
	Enabled    bool   `gorm:"index"`
	AcceptAny  bool
	Allow      string
	NotifyChat string `gorm:"size:64"`
	Token      []byte
	Created    time.Time
	Updated    time.Time
}

func (bindingRow) TableName() string { return "studio_connectors" }

type stateRow struct {
	Binding string `gorm:"primaryKey;size:64"`
	Offset  int64
}

func (stateRow) TableName() string { return "studio_connector_state" }

type senderRow struct {
	Binding string `gorm:"primaryKey;size:64"`
	ID      string `gorm:"primaryKey;size:64"`
	Name    string `gorm:"size:255"`
	Count   int
	First   time.Time
	Last    time.Time `gorm:"index"`
	Allowed bool
}

func (senderRow) TableName() string { return "studio_connector_senders" }

type chatRow struct {
	Binding string `gorm:"primaryKey;size:64"`
	Sender  string `gorm:"primaryKey;size:64"`
	ChatID  string `gorm:"size:128"`
}

func (chatRow) TableName() string { return "studio_connector_chats" }

type keyRow struct {
	Name  string `gorm:"primaryKey;size:32"`
	Value []byte
}

func (keyRow) TableName() string { return "studio_connector_keys" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("connectors: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&bindingRow{}, &stateRow{}, &senderRow{}, &chatRow{}, &keyRow{}); err != nil {
		return nil, fmt.Errorf("connectors: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (r bindingRow) binding() (Binding, error) {
	b := Binding{ID: r.ID, Owner: r.Owner, Kind: r.Kind, AddonID: r.AddonID, Name: r.Name, AgentID: r.AgentID, Enabled: r.Enabled, AcceptAny: r.AcceptAny,
		NotifyChat: r.NotifyChat, Token: append([]byte(nil), r.Token...), Created: r.Created.UTC(), Updated: r.Updated.UTC()}
	if r.Allow != "" {
		if err := json.Unmarshal([]byte(r.Allow), &b.Allow); err != nil {
			return b, err
		}
	}
	return Normalize(b), nil
}

func (s *gormStore) PutBinding(ctx context.Context, b Binding) error {
	allow := ""
	if len(b.Allow) > 0 {
		raw, _ := json.Marshal(b.Allow)
		allow = string(raw)
	}
	return s.db.WithContext(ctx).Save(&bindingRow{ID: b.ID, Owner: b.Owner, Kind: b.Kind, AddonID: b.AddonID, Name: b.Name, AgentID: b.AgentID, Enabled: b.Enabled,
		AcceptAny: b.AcceptAny, Allow: allow, NotifyChat: b.NotifyChat, Token: b.Token, Created: b.Created, Updated: b.Updated}).Error
}

func (s *gormStore) GetBinding(ctx context.Context, id string) (Binding, error) {
	var r bindingRow
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Binding{}, ErrNotFound
	}
	if err != nil {
		return Binding{}, err
	}
	return r.binding()
}

func (s *gormStore) list(q *gorm.DB) ([]Binding, error) {
	var rows []bindingRow
	if err := q.Order("name, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Binding, 0, len(rows))
	for _, r := range rows {
		b, err := r.binding()
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *gormStore) ListBindings(ctx context.Context, owner string) ([]Binding, error) {
	return s.list(s.db.WithContext(ctx).Where("owner = ?", owner))
}

func (s *gormStore) EnabledBindings(ctx context.Context) ([]Binding, error) {
	return s.list(s.db.WithContext(ctx).Where("enabled = ?", true))
}

func (s *gormStore) DeleteBinding(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&stateRow{}, &senderRow{}, &chatRow{}} {
			if err := tx.Where("binding = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ?", id).Delete(&bindingRow{}).Error
	})
}

func (s *gormStore) Offset(ctx context.Context, id string) (int64, bool, error) {
	var r stateRow
	err := s.db.WithContext(ctx).Where("binding = ?", id).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	return r.Offset, err == nil, err
}

func (s *gormStore) SetOffset(ctx context.Context, id string, offset int64) error {
	return s.db.WithContext(ctx).Save(&stateRow{Binding: id, Offset: offset}).Error
}

func (s *gormStore) RecordSender(ctx context.Context, binding, senderID, name string, allowed bool, at time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r senderRow
		err := tx.Where("binding = ? AND id = ?", binding, senderID).Take(&r).Error
		switch {
		case err == nil:
			r.Count, r.Name, r.Last, r.Allowed = r.Count+1, name, at.UTC(), allowed
		case errors.Is(err, gorm.ErrRecordNotFound):
			r = senderRow{Binding: binding, ID: senderID, Name: name, Count: 1, First: at.UTC(), Last: at.UTC(), Allowed: allowed}
		default:
			return err
		}
		if err := tx.Save(&r).Error; err != nil {
			return err
		}
		var edge []time.Time
		if err := tx.Model(&senderRow{}).Where("binding = ?", binding).Order("last DESC, id DESC").
			Offset(MaxSendersKept-1).Limit(1).Pluck("last", &edge).Error; err != nil {
			return err
		}
		if len(edge) == 1 {
			return tx.Where("binding = ? AND last < ?", binding, edge[0]).Delete(&senderRow{}).Error
		}
		return nil
	})
}

func (s *gormStore) Senders(ctx context.Context, binding string, limit int) ([]Sender, error) {
	q := s.db.WithContext(ctx).Where("binding = ?", binding).Order("last DESC, id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []senderRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Sender, len(rows))
	for i, r := range rows {
		out[i] = Sender{Binding: r.Binding, ID: r.ID, Name: r.Name, Count: r.Count, First: r.First.UTC(), Last: r.Last.UTC(), Allowed: r.Allowed}
	}
	return out, nil
}

func (s *gormStore) ChatOf(ctx context.Context, binding, senderID string) (string, bool, error) {
	var r chatRow
	err := s.db.WithContext(ctx).Where("binding = ? AND sender = ?", binding, senderID).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	return r.ChatID, err == nil, err
}

func (s *gormStore) SetChat(ctx context.Context, binding, senderID, chatID string) error {
	return s.db.WithContext(ctx).Save(&chatRow{Binding: binding, Sender: senderID, ChatID: chatID}).Error
}

func (s *gormStore) EnsureKey(ctx context.Context, generate func() ([]byte, error)) ([]byte, error) {
	read := func() ([]byte, bool, error) {
		var r keyRow
		err := s.db.WithContext(ctx).Where("name = ?", "seal").Take(&r).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return r.Value, err == nil, err
	}
	if k, ok, err := read(); err != nil || ok {
		return k, err
	}
	k, err := generate()
	if err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&keyRow{Name: "seal", Value: k}).Error; err != nil {
		return nil, err
	}
	got, ok, err := read()
	if err != nil || !ok {
		return nil, fmt.Errorf("connectors: the sealing key was not stored: %v", err)
	}
	return got, nil
}
