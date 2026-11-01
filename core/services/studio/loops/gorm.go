package loops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type loopRow struct {
	ID         string `gorm:"primaryKey;size:128"`
	Owner      string `gorm:"index:idx_studio_chat_loops_chat,priority:1;size:255;not null"`
	ChatID     string `gorm:"index:idx_studio_chat_loops_chat,priority:2;size:255;not null"`
	Prompt     string `gorm:"type:text"`
	IntervalNs int64
	State      string `gorm:"index;size:16"`
	Reason     string `gorm:"type:text"`
	Runs       int
	Failures   int
	Next       time.Time
	Running    bool `gorm:"index"`
	Scheduled  bool
	Wait       string `gorm:"type:text"`
	Created    time.Time
	Updated    time.Time
	LastRun    time.Time
	// Rev is bumped by Update; the bump takes the row lock, so a second
	// frontend's change waits for the first and then reads what it wrote.
	Rev int64
}

func (loopRow) TableName() string { return "studio_chat_loops" }

// ownerRow is the daily budget of one owner and the row that Add and TakeRun
// lock, so that two frontends count the owner's loops and runs one at a time.
type ownerRow struct {
	Owner   string `gorm:"primaryKey;size:255"`
	Rev     int64
	Day     string `gorm:"size:16"`
	DayRuns int
}

func (ownerRow) TableName() string { return "studio_chat_loop_owners" }

type consentRow struct {
	Owner  string `gorm:"primaryKey;size:255"`
	ChatID string `gorm:"primaryKey;size:255"`
	At     time.Time
}

func (consentRow) TableName() string { return "studio_chat_loop_consents" }

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("loops: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&loopRow{}, &ownerRow{}, &consentRow{}); err != nil {
		return nil, fmt.Errorf("loops: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func utc(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

func toRow(l Loop) loopRow {
	return loopRow{ID: l.ID, Owner: l.Owner, ChatID: l.ChatID, Prompt: l.Prompt, IntervalNs: int64(l.Interval),
		State: string(l.State), Reason: l.Reason, Runs: l.Runs, Failures: l.Failures, Next: utc(l.Next),
		Running: l.Running, Scheduled: l.Scheduled, Wait: l.Wait, Created: utc(l.Created), Updated: utc(l.Updated),
		LastRun: utc(l.LastRun)}
}

func (r loopRow) loop() Loop {
	return Loop{ID: r.ID, Owner: r.Owner, ChatID: r.ChatID, Prompt: r.Prompt, Interval: time.Duration(r.IntervalNs),
		State: State(r.State), Reason: r.Reason, Runs: r.Runs, Failures: r.Failures, Next: utc(r.Next),
		Running: r.Running, Scheduled: r.Scheduled, Wait: r.Wait, Created: utc(r.Created), Updated: utc(r.Updated),
		LastRun: utc(r.LastRun)}
}

// lockOwner creates the owner's row when it is missing and takes its lock.
func lockOwner(tx *gorm.DB, owner string) (ownerRow, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ownerRow{Owner: owner}).Error; err != nil {
		return ownerRow{}, err
	}
	if err := tx.Model(&ownerRow{}).Where("owner = ?", owner).UpdateColumn("rev", gorm.Expr("rev + 1")).Error; err != nil {
		return ownerRow{}, err
	}
	var r ownerRow
	err := tx.Where("owner = ?", owner).Take(&r).Error
	return r, err
}

func (s *gormStore) Add(ctx context.Context, l Loop, lim Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockOwner(tx, l.Owner); err != nil {
			return err
		}
		var exists int64
		if err := tx.Model(&loopRow{}).Where("id = ?", l.ID).Count(&exists).Error; err != nil {
			return err
		}
		if exists > 0 {
			return fmt.Errorf("%w: loop %s exists", ErrInvalid, l.ID)
		}
		live := []string{string(StatePending), string(StateActive), string(StatePaused)}
		var ofOwner, inChat int64
		if err := tx.Model(&loopRow{}).Where("owner = ? AND state IN ?", l.Owner, live).Count(&ofOwner).Error; err != nil {
			return err
		}
		if err := tx.Model(&loopRow{}).Where("owner = ? AND chat_id = ? AND state IN ?", l.Owner, l.ChatID, live).Count(&inChat).Error; err != nil {
			return err
		}
		if err := checkLimits(int(inChat), int(ofOwner), lim); err != nil {
			return err
		}
		row := toRow(l)
		return tx.Create(&row).Error
	})
}

func (s *gormStore) Get(ctx context.Context, id string) (Loop, error) {
	var r loopRow
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Loop{}, ErrNotFound
	}
	if err != nil {
		return Loop{}, err
	}
	return r.loop(), nil
}

func (s *gormStore) Update(ctx context.Context, id string, change func(*Loop) error) (Loop, error) {
	var out Loop
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&loopRow{}).Where("id = ?", id).UpdateColumn("rev", gorm.Expr("rev + 1"))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		var r loopRow
		if err := tx.Where("id = ?", id).Take(&r).Error; err != nil {
			return err
		}
		cur := r.loop()
		next := cur
		if err := change(&next); err != nil {
			return err
		}
		next.ID, next.Owner, next.ChatID = cur.ID, cur.Owner, cur.ChatID
		if err := next.Validate(); err != nil {
			return err
		}
		row := toRow(next)
		row.Rev = r.Rev
		if err := tx.Select("*").Save(&row).Error; err != nil {
			return err
		}
		out = row.loop()
		return nil
	})
	return out, err
}

func (s *gormStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&loopRow{}).Error
}

func (s *gormStore) list(q *gorm.DB) ([]Loop, error) {
	var rows []loopRow
	if err := q.Order("created, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Loop, len(rows))
	for i, r := range rows {
		out[i] = r.loop()
	}
	return out, nil
}

func (s *gormStore) ListChat(ctx context.Context, owner, chatID string) ([]Loop, error) {
	return s.list(s.db.WithContext(ctx).Where("owner = ? AND chat_id = ?", owner, chatID))
}

func (s *gormStore) ListOwner(ctx context.Context, owner string) ([]Loop, error) {
	return s.list(s.db.WithContext(ctx).Where("owner = ?", owner))
}

func (s *gormStore) Working(ctx context.Context) ([]Loop, error) {
	return s.list(s.db.WithContext(ctx).Where("state = ? OR running = ?", string(StateActive), true))
}

func (s *gormStore) DeleteChat(ctx context.Context, owner, chatID string) (int, error) {
	var n int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("owner = ? AND chat_id = ?", owner, chatID).Delete(&loopRow{})
		if res.Error != nil {
			return res.Error
		}
		n = res.RowsAffected
		return tx.Where("owner = ? AND chat_id = ?", owner, chatID).Delete(&consentRow{}).Error
	})
	return int(n), err
}

func (s *gormStore) TakeRun(ctx context.Context, owner, day string, budget int) (bool, error) {
	took := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := lockOwner(tx, owner)
		if err != nil {
			return err
		}
		if r.Day != day {
			r.Day, r.DayRuns = day, 0
		}
		if budget > 0 && r.DayRuns >= budget {
			return nil
		}
		took = true
		return tx.Model(&ownerRow{}).Where("owner = ?", owner).
			Updates(map[string]any{"day": r.Day, "day_runs": r.DayRuns + 1}).Error
	})
	return took, err
}

func (s *gormStore) Consented(ctx context.Context, owner, chatID string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&consentRow{}).Where("owner = ? AND chat_id = ?", owner, chatID).Count(&n).Error
	return n > 0, err
}

func (s *gormStore) SetConsented(ctx context.Context, owner, chatID string) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&consentRow{Owner: owner, ChatID: chatID, At: time.Now().UTC()}).Error
}
