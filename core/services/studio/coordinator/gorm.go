package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// configRow is the table of coordinator configs. The contacts are JSON text so
// one schema works on SQLite and PostgreSQL.
type configRow struct {
	AgentID       string `gorm:"primaryKey;size:64"`
	Principal     string `gorm:"index;size:255"`
	ChatID        string `gorm:"size:64"`
	Page          string `gorm:"size:64"`
	Contacts      string
	WakeEvery     int64
	WakeNote      string
	SendPerMinute int
	SendPerDay    int
	MaxHelpers    int
	MaxDepth      int
	Paused        bool
	PausedBy      string `gorm:"size:255"`
	PausedAt      time.Time
	NextWake      time.Time
	LastWake      time.Time
	Created       time.Time
	Updated       time.Time
}

func (configRow) TableName() string { return "studio_coordinators" }

// counterRow is one counter. The pair (AgentID, Bucket) is the key.
type counterRow struct {
	AgentID string `gorm:"primaryKey;size:64"`
	Bucket  string `gorm:"primaryKey;size:32"`
	N       int    `gorm:"not null;default:0"`
}

func (counterRow) TableName() string { return "studio_coordinator_counters" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("coordinator: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&configRow{}, &counterRow{}); err != nil {
		return nil, fmt.Errorf("coordinator: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (r configRow) config() (Config, error) {
	c := Config{
		AgentID: r.AgentID, Principal: r.Principal, ChatID: r.ChatID, Page: r.Page,
		WakeEvery: time.Duration(r.WakeEvery), WakeNote: r.WakeNote,
		SendPerMinute: r.SendPerMinute, SendPerDay: r.SendPerDay, MaxHelpers: r.MaxHelpers, MaxDepth: r.MaxDepth,
		Paused: r.Paused, PausedBy: r.PausedBy,
		PausedAt: r.PausedAt.UTC(), NextWake: r.NextWake.UTC(), LastWake: r.LastWake.UTC(),
		Created: r.Created.UTC(), Updated: r.Updated.UTC(),
	}
	if r.Contacts != "" {
		if err := json.Unmarshal([]byte(r.Contacts), &c.Contacts); err != nil {
			return c, fmt.Errorf("coordinator: contacts of %s: %w", r.AgentID, err)
		}
	}
	return c, nil
}

func rowOf(c Config) configRow {
	contacts := ""
	if len(c.Contacts) > 0 {
		raw, _ := json.Marshal(c.Contacts)
		contacts = string(raw)
	}
	return configRow{
		AgentID: c.AgentID, Principal: c.Principal, ChatID: c.ChatID, Page: c.Page, Contacts: contacts,
		WakeEvery: int64(c.WakeEvery), WakeNote: c.WakeNote,
		SendPerMinute: c.SendPerMinute, SendPerDay: c.SendPerDay, MaxHelpers: c.MaxHelpers, MaxDepth: c.MaxDepth,
		Paused: c.Paused, PausedBy: c.PausedBy, PausedAt: c.PausedAt, NextWake: c.NextWake, LastWake: c.LastWake,
		Created: c.Created, Updated: c.Updated,
	}
}

func (s *gormStore) Put(ctx context.Context, c Config) error {
	if c.AgentID == "" {
		return invalid("an agent ID is required")
	}
	if err := Validate(c); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old configRow
		err := tx.Where("agent_id = ?", c.AgentID).Take(&old).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(ptr(rowOf(c))).Error
		}
		if err != nil {
			return err
		}
		// Only the fields the creator sets: the runtime columns stay.
		r := rowOf(c)
		return tx.Model(&configRow{}).Where("agent_id = ?", c.AgentID).Updates(map[string]any{
			"principal": r.Principal, "page": r.Page, "contacts": r.Contacts,
			"wake_every": r.WakeEvery, "wake_note": r.WakeNote,
			"send_per_minute": r.SendPerMinute, "send_per_day": r.SendPerDay,
			"max_helpers": r.MaxHelpers, "max_depth": r.MaxDepth, "updated": r.Updated,
		}).Error
	})
}

func ptr[T any](v T) *T { return &v }

func (s *gormStore) Get(ctx context.Context, agentID string) (Config, error) {
	var r configRow
	err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, err
	}
	return r.config()
}

func (s *gormStore) List(ctx context.Context) ([]Config, error) {
	var rows []configRow
	if err := s.db.WithContext(ctx).Order("agent_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Config, 0, len(rows))
	for _, r := range rows {
		c, err := r.config()
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *gormStore) Delete(ctx context.Context, agentID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("agent_id = ?", agentID).Delete(&configRow{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Where("agent_id = ?", agentID).Delete(&counterRow{}).Error
	})
}

func (s *gormStore) SetChat(ctx context.Context, agentID, chatID string) (string, error) {
	res := s.db.WithContext(ctx).Model(&configRow{}).
		Where("agent_id = ? AND chat_id = ''", agentID).Update("chat_id", chatID)
	if res.Error != nil {
		return "", res.Error
	}
	c, err := s.Get(ctx, agentID)
	return c.ChatID, err
}

func (s *gormStore) SetPaused(ctx context.Context, agentID string, paused bool, by string, at time.Time) (Config, bool, error) {
	upd := map[string]any{"paused": paused, "paused_by": "", "paused_at": time.Time{}}
	if paused {
		upd["paused_by"], upd["paused_at"] = by, at.UTC()
	}
	res := s.db.WithContext(ctx).Model(&configRow{}).
		Where("agent_id = ? AND paused = ?", agentID, !paused).Updates(upd)
	if res.Error != nil {
		return Config{}, false, res.Error
	}
	c, err := s.Get(ctx, agentID)
	return c, err == nil && res.RowsAffected == 1, err
}

func (s *gormStore) ClaimWake(ctx context.Context, agentID string, now, next time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&configRow{}).
		Where("agent_id = ? AND next_wake <= ?", agentID, now.UTC()).
		Updates(map[string]any{"next_wake": next.UTC(), "last_wake": now.UTC()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (s *gormStore) Reserve(ctx context.Context, agentID, bucket string, limit int) (bool, error) {
	db := s.db.WithContext(ctx)
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&counterRow{AgentID: agentID, Bucket: bucket}).Error; err != nil {
		return false, err
	}
	res := db.Model(&counterRow{}).
		Where("agent_id = ? AND bucket = ? AND n < ?", agentID, bucket, limit).
		UpdateColumn("n", gorm.Expr("n + 1"))
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (s *gormStore) Used(ctx context.Context, agentID, bucket string) (int, error) {
	var r counterRow
	err := s.db.WithContext(ctx).Where("agent_id = ? AND bucket = ?", agentID, bucket).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return r.N, err
}
