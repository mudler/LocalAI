package triggers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type triggerRow struct {
	ID           string `gorm:"primaryKey;size:128"`
	Owner        string `gorm:"index;size:255;not null"`
	Name         string `gorm:"size:255"`
	Prompt       string
	Enabled      bool   `gorm:"index"`
	Kind         string `gorm:"size:16"`
	Schedule     string `gorm:"size:255"`
	TZ           string `gorm:"column:tz;size:64"`
	Folder       string `gorm:"size:1024"`
	QuietNs      int64
	DailyCap     int
	Since        time.Time
	AgentID      string `gorm:"size:255"`
	SourceChatID string `gorm:"size:255"`
	Model        string `gorm:"size:255"`
	Proposed     bool
}

func (triggerRow) TableName() string { return "studio_triggers" }

// runRow has a numeric key so that claim order is the key order. The unique
// index on (trigger, due key) is what makes a due time run once across
// frontends.
type runRow struct {
	Seq       int64  `gorm:"primaryKey;autoIncrement"`
	ID        string `gorm:"uniqueIndex;size:128;not null"`
	TriggerID string `gorm:"uniqueIndex:idx_studio_trigger_runs_due,priority:1;size:128;not null"`
	DueKey    string `gorm:"uniqueIndex:idx_studio_trigger_runs_due,priority:2;size:512;not null"`
	Due       time.Time
	Status    string `gorm:"index;size:16"`
	Reason    string
	ChatID    string `gorm:"index;size:255"`
	File      string `gorm:"size:1024"`
	Started   time.Time
	Ended     time.Time
}

func (runRow) TableName() string { return "studio_trigger_runs" }

// stateRow holds the Runner's cursor and the last due time of each schedule trigger.
type stateRow struct {
	Key   string `gorm:"primaryKey;size:255"`
	Value string
}

func (stateRow) TableName() string { return "studio_trigger_state" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("triggers: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&triggerRow{}, &runRow{}, &stateRow{}); err != nil {
		return nil, fmt.Errorf("triggers: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

const (
	cursorKey  = "cursor"
	lastDuePre = "last_due/"
)

func (r triggerRow) trigger() Trigger {
	return Trigger{ID: r.ID, Owner: r.Owner, Name: r.Name, Prompt: r.Prompt, Enabled: r.Enabled, Kind: Kind(r.Kind),
		Schedule: r.Schedule, TZ: r.TZ, Folder: r.Folder, Quiet: time.Duration(r.QuietNs), DailyCap: r.DailyCap,
		Since: utc(r.Since), AgentID: r.AgentID, SourceChatID: r.SourceChatID, Model: r.Model, Proposed: r.Proposed}
}

func (s *gormStore) PutTrigger(ctx context.Context, t Trigger) error {
	row := triggerRow{ID: t.ID, Owner: t.Owner, Name: t.Name, Prompt: t.Prompt, Enabled: t.Enabled, Kind: string(t.Kind),
		Schedule: t.Schedule, TZ: t.TZ, Folder: t.Folder, QuietNs: int64(t.Quiet), DailyCap: t.DailyCap,
		Since: utc(t.Since), AgentID: t.AgentID, SourceChatID: t.SourceChatID, Model: t.Model, Proposed: t.Proposed}
	return s.db.WithContext(ctx).Save(&row).Error
}

func (s *gormStore) DeleteTrigger(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("trigger_id = ?", id).Delete(&runRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("key = ?", lastDuePre+id).Delete(&stateRow{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&triggerRow{}).Error
	})
}

func (s *gormStore) GetTrigger(ctx context.Context, id string) (Trigger, error) {
	var r triggerRow
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Trigger{}, ErrNotFound
	}
	if err != nil {
		return Trigger{}, err
	}
	return r.trigger(), nil
}

func (s *gormStore) listTriggers(q *gorm.DB) ([]Trigger, error) {
	var rows []triggerRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Trigger, len(rows))
	for i, r := range rows {
		out[i] = r.trigger()
	}
	return out, nil
}

func (s *gormStore) ListTriggers(ctx context.Context, owner string) ([]Trigger, error) {
	return s.listTriggers(s.db.WithContext(ctx).Where("owner = ?", owner).Order("name, id"))
}

func (s *gormStore) AllTriggers(ctx context.Context) ([]Trigger, error) {
	return s.listTriggers(s.db.WithContext(ctx).Order("owner, name, id"))
}

func (s *gormStore) EnabledTriggers(ctx context.Context) ([]Trigger, error) {
	return s.listTriggers(s.db.WithContext(ctx).Where("enabled = ?", true).Order("id"))
}

func (r runRow) run() Run {
	return Run{ID: r.ID, TriggerID: r.TriggerID, DueKey: r.DueKey, Due: utc(r.Due), Status: Status(r.Status),
		Reason: r.Reason, ChatID: r.ChatID, File: r.File, Started: utc(r.Started), Ended: utc(r.Ended)}
}

func isUniqueViolation(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate key")
}

// Claim inserts the run. The unique index on (trigger, due key) refuses a
// second run for the same time or file version, whoever asks and whenever, so
// two frontends cannot both start it.
func (s *gormStore) Claim(ctx context.Context, r Run) (bool, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&runRow{}).Where("id = ?", r.ID).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		var same int64
		if err := s.db.WithContext(ctx).Model(&runRow{}).Where("trigger_id = ? AND due_key = ?", r.TriggerID, r.DueKey).Count(&same).Error; err != nil {
			return false, err
		}
		if same > 0 {
			return false, nil
		}
		return false, fmt.Errorf("triggers: run %s already exists", r.ID)
	}
	row := runRow{ID: r.ID, TriggerID: r.TriggerID, DueKey: r.DueKey, Due: utc(r.Due), Status: string(r.Status),
		Reason: r.Reason, ChatID: r.ChatID, File: r.File, Started: utc(r.Started), Ended: utc(r.Ended)}
	err := s.db.WithContext(ctx).Create(&row).Error
	if err == nil {
		return true, nil
	}
	if isUniqueViolation(err) {
		var same int64
		if cerr := s.db.WithContext(ctx).Model(&runRow{}).Where("trigger_id = ? AND due_key = ?", r.TriggerID, r.DueKey).Count(&same).Error; cerr == nil && same > 0 {
			return false, nil
		}
	}
	return false, err
}

func (s *gormStore) GetRun(ctx context.Context, id string) (Run, error) {
	var r runRow
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return r.run(), nil
}

func (s *gormStore) SetRunStatus(ctx context.Context, r Run) error {
	res := s.db.WithContext(ctx).Model(&runRow{}).Where("id = ?", r.ID).Updates(map[string]any{
		"status": string(r.Status), "reason": r.Reason, "chat_id": r.ChatID, "started": utc(r.Started), "ended": utc(r.Ended)})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *gormStore) runs(q *gorm.DB) ([]Run, error) {
	var rows []runRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = r.run()
	}
	return out, nil
}

func (s *gormStore) ListRuns(ctx context.Context, triggerID string, limit int) ([]Run, error) {
	q := s.db.WithContext(ctx).Where("trigger_id = ?", triggerID).Order("seq DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return s.runs(q)
}

func (s *gormStore) RunningRuns(ctx context.Context) ([]Run, error) {
	return s.runs(s.db.WithContext(ctx).Where("status = ?", string(StatusRunning)).Order("seq"))
}

func (s *gormStore) QueuedRuns(ctx context.Context) ([]Run, error) {
	return s.runs(s.db.WithContext(ctx).Where("status = ?", string(StatusQueued)).Order("seq"))
}

func (s *gormStore) RunByChat(ctx context.Context, chatID string) (Run, error) {
	if chatID == "" {
		return Run{}, ErrNotFound
	}
	var r runRow
	err := s.db.WithContext(ctx).Where("chat_id = ?", chatID).Order("seq").Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return r.run(), nil
}

func (s *gormStore) CountRunsSince(ctx context.Context, triggerID string, since time.Time) (int, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&runRow{}).
		Where("trigger_id = ? AND status <> ? AND due >= ?", triggerID, string(StatusSkipped), utc(since)).Count(&n).Error
	return int(n), err
}

func (s *gormStore) state(ctx context.Context, key string) (string, bool, error) {
	var r stateRow
	err := s.db.WithContext(ctx).Where("key = ?", key).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	return r.Value, err == nil, err
}

func (s *gormStore) setState(ctx context.Context, key, value string) error {
	return s.db.WithContext(ctx).Save(&stateRow{Key: key, Value: value}).Error
}

func (s *gormStore) Cursor(ctx context.Context) (int64, bool, error) {
	v, ok, err := s.state(ctx, cursorKey)
	if err != nil || !ok {
		return 0, false, err
	}
	rev, err := strconv.ParseInt(v, 10, 64)
	return rev, err == nil, err
}

func (s *gormStore) SetCursor(ctx context.Context, rev int64) error {
	return s.setState(ctx, cursorKey, strconv.FormatInt(rev, 10))
}

func (s *gormStore) LastDue(ctx context.Context, id string) (time.Time, error) {
	v, ok, err := s.state(ctx, lastDuePre+id)
	if err != nil || !ok {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, v)
}

func (s *gormStore) SetLastDue(ctx context.Context, id string, t time.Time) error {
	return s.setState(ctx, lastDuePre+id, utc(t).Format(time.RFC3339Nano))
}
