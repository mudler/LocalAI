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
	ID        string `gorm:"primaryKey;size:64"`
	Owner     string `gorm:"index;size:255;not null"`
	Kind      string `gorm:"size:32"`
	AddonID   string `gorm:"size:64"`
	Name      string `gorm:"size:255"`
	AgentID   string `gorm:"size:128"`
	Enabled   bool   `gorm:"index"`
	AcceptAny bool
	Allow     string
	// AllowTrust is JSON, added by AutoMigrate; a row stored before it trusts no chat.
	AllowTrust string
	NotifyChat string `gorm:"size:64"`
	// The columns of the reply and conversation modes are added by
	// AutoMigrate; a row stored before them reads as the defaults.
	Reply         string `gorm:"size:16"`
	Conversation  string `gorm:"size:16"`
	WindowMinutes int
	FilesOff      bool
	// ReplyLanguage is added by AutoMigrate; a row stored before it reads as auto.
	ReplyLanguage string `gorm:"size:8"`
	// UILanguage is added by AutoMigrate; a row stored before it reads as unknown.
	UILanguage string `gorm:"size:35"`
	// A thread_context_off column that an earlier build added stays in the
	// table, unread and unwritten: AutoMigrate never drops a column, and the
	// column accepts NULL, so an insert without it works.
	// Options is JSON, added by AutoMigrate.
	Options string
	// Resources is JSON, added by AutoMigrate; a row stored before it lists none.
	Resources string
	// Channels is JSON, added by AutoMigrate; a row stored before it lists none.
	Channels string
	// WriteMode is added by AutoMigrate; a row stored before it reads as off.
	WriteMode string `gorm:"size:16"`
	// Approval is JSON, added by AutoMigrate; a row stored before it asks.
	Approval string
	// The columns of the file limits are added by AutoMigrate; a row stored
	// before them has no limit of the owner.
	FileReceiveMax int64
	FileSendMax    int64
	// Caps, Grants and Paired are JSON, added by AutoMigrate; a row stored
	// before them has none.
	Caps   string
	Grants string
	Paired string
	// WriteTickRemoved is added by AutoMigrate; a row stored before it reads as false.
	WriteTickRemoved bool
	// AccountID is added by AutoMigrate; a row stored before it has none.
	AccountID string `gorm:"size:128"`
	Token     []byte
	Created   time.Time
	Updated   time.Time
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

type pairingRow struct {
	Binding string `gorm:"primaryKey;size:64"`
	Hash    string `gorm:"size:64"`
	Expires time.Time
}

func (pairingRow) TableName() string { return "studio_connector_pairings" }

type keyRow struct {
	Name  string `gorm:"primaryKey;size:32"`
	Value []byte
}

func (keyRow) TableName() string { return "studio_connector_keys" }

// migrationRow records a one-time migration. Done is set when it finished;
// until then Started is the lease of the process that runs it.
type migrationRow struct {
	Name     string `gorm:"primaryKey;size:64"`
	Started  time.Time
	Finished time.Time
	Done     bool
}

func (migrationRow) TableName() string { return "studio_connector_migrations" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("connectors: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&bindingRow{}, &stateRow{}, &senderRow{}, &chatRow{}, &keyRow{}, &pairingRow{}, &migrationRow{}); err != nil {
		return nil, fmt.Errorf("connectors: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (r bindingRow) binding() (Binding, error) {
	b := Binding{ID: r.ID, Owner: r.Owner, Kind: r.Kind, AddonID: r.AddonID, Name: r.Name, AgentID: r.AgentID, Enabled: r.Enabled, AcceptAny: r.AcceptAny,
		NotifyChat: r.NotifyChat, Reply: r.Reply, Conversation: r.Conversation, WindowMinutes: r.WindowMinutes, FilesOff: r.FilesOff, ReplyLanguage: r.ReplyLanguage, UILanguage: r.UILanguage, WriteMode: r.WriteMode, AccountID: r.AccountID, WriteTickRemoved: r.WriteTickRemoved,
		FileReceiveMax: r.FileReceiveMax, FileSendMax: r.FileSendMax,
		Token: append([]byte(nil), r.Token...), Created: r.Created.UTC(), Updated: r.Updated.UTC()}
	if r.Allow != "" {
		if err := json.Unmarshal([]byte(r.Allow), &b.Allow); err != nil {
			return b, err
		}
	}
	if r.Options != "" {
		if err := json.Unmarshal([]byte(r.Options), &b.Options); err != nil {
			return b, err
		}
	}
	for _, f := range []struct {
		raw string
		dst any
	}{{r.Caps, &b.Caps}, {r.AllowTrust, &b.AllowTrust}, {r.Grants, &b.Grants}, {r.Paired, &b.Paired}, {r.Resources, &b.Resources}, {r.Channels, &b.Channels}, {r.Approval, &b.Approval}} {
		if f.raw == "" {
			continue
		}
		if err := json.Unmarshal([]byte(f.raw), f.dst); err != nil {
			return b, err
		}
	}
	return b, nil
}

func (s *gormStore) PutBinding(ctx context.Context, b Binding) error {
	allow := ""
	if len(b.Allow) > 0 {
		raw, _ := json.Marshal(b.Allow)
		allow = string(raw)
	}
	options := ""
	if len(b.Options) > 0 {
		raw, _ := json.Marshal(b.Options)
		options = string(raw)
	}
	enc := func(v any, empty bool) string {
		if empty {
			return ""
		}
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	return s.db.WithContext(ctx).Save(&bindingRow{AllowTrust: enc(b.AllowTrust, len(b.AllowTrust) == 0), Approval: enc(b.Approval, b.Approval.IsZero()), WriteMode: b.WriteMode, Resources: enc(b.Resources, len(b.Resources) == 0), Channels: enc(b.Channels, len(b.Channels) == 0), Caps: enc(b.Caps, len(b.Caps) == 0), WriteTickRemoved: b.WriteTickRemoved, Grants: enc(b.Grants, len(b.Grants) == 0), Paired: enc(b.Paired, len(b.Paired) == 0), ID: b.ID, Owner: b.Owner, Kind: b.Kind, AddonID: b.AddonID, Name: b.Name, AgentID: b.AgentID, Enabled: b.Enabled,
		AcceptAny: b.AcceptAny, Allow: allow, NotifyChat: b.NotifyChat, Reply: b.Reply, Conversation: b.Conversation, WindowMinutes: b.WindowMinutes,
		FilesOff: b.FilesOff, ReplyLanguage: b.ReplyLanguage, UILanguage: b.UILanguage, AccountID: b.AccountID, Options: options, FileReceiveMax: b.FileReceiveMax, FileSendMax: b.FileSendMax,
		Token: b.Token, Created: b.Created, Updated: b.Updated}).Error
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

func (s *gormStore) AllBindings(ctx context.Context) ([]Binding, error) {
	return s.list(s.db.WithContext(ctx))
}

func (s *gormStore) EnabledBindings(ctx context.Context) ([]Binding, error) {
	return s.list(s.db.WithContext(ctx).Where("enabled = ?", true))
}

func (s *gormStore) DeleteBinding(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&stateRow{}, &senderRow{}, &chatRow{}, &pairingRow{}} {
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

func (s *gormStore) ClearChat(ctx context.Context, binding, senderID string) error {
	return s.db.WithContext(ctx).Where("binding = ? AND sender = ?", binding, senderID).Delete(&chatRow{}).Error
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
	if err != nil {
		return nil, fmt.Errorf("connectors: the sealing key was not stored: %v", err)
	}
	if !ok {
		return nil, errors.New("connectors: the sealing key was not stored")
	}
	return got, nil
}

func (s *gormStore) PutPairing(ctx context.Context, binding string, p Pairing) error {
	return s.db.WithContext(ctx).Save(&pairingRow{Binding: binding, Hash: p.Hash, Expires: p.Expires.UTC()}).Error
}

func (s *gormStore) ConsumePairing(ctx context.Context, binding, hash string, now time.Time) (bool, error) {
	// An expired code stays until it is replaced or the binding goes; it
	// cannot match.
	res := s.db.WithContext(ctx).Where("binding = ? AND hash = ? AND expires > ?", binding, hash, now.UTC()).Delete(&pairingRow{})
	return res.RowsAffected == 1, res.Error
}

func (s *gormStore) SetCapsIfEmpty(ctx context.Context, id string, caps []string) (bool, error) {
	if len(caps) == 0 {
		return false, nil
	}
	raw, _ := json.Marshal(caps)
	// Caps is "" when a row has none (PutBinding writes it so), NULL for a row
	// from before the column existed, and "[]" is never written but is
	// accepted for safety.
	res := s.db.WithContext(ctx).Model(&bindingRow{}).
		Where("id = ? AND (caps IS NULL OR caps = '' OR caps = '[]' OR caps = 'null')", id).
		Update("caps", string(raw))
	return res.RowsAffected == 1, res.Error
}

func (s *gormStore) SetAccountID(ctx context.Context, id, accountID string) error {
	return s.db.WithContext(ctx).Model(&bindingRow{}).Where("id = ?", id).Update("account_id", accountID).Error
}

func (s *gormStore) ClaimMigration(ctx context.Context, name string, now time.Time, lease time.Duration) (bool, error) {
	db := s.db.WithContext(ctx)
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&migrationRow{Name: name, Started: now.UTC()})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		return true, nil
	}
	// A claim that was never finished and has outlived its lease is taken
	// over with a conditional update, so two takers cannot both win.
	res = db.Model(&migrationRow{}).Where("name = ? AND done = ? AND started <= ?", name, false, now.Add(-lease).UTC()).
		Update("started", now.UTC())
	return res.RowsAffected == 1, res.Error
}

func (s *gormStore) FinishMigration(ctx context.Context, name string, now time.Time) error {
	return s.db.WithContext(ctx).Model(&migrationRow{}).Where("name = ?", name).
		Updates(map[string]any{"done": true, "finished": now.UTC()}).Error
}

func (s *gormStore) ReleaseMigration(ctx context.Context, name string) error {
	return s.db.WithContext(ctx).Where("name = ? AND done = ?", name, false).Delete(&migrationRow{}).Error
}
