package hostexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// The audit is kept like the audit of the shared folders: the auth database in
// a cluster, so every frontend writes the same audit, or a file under the data
// path, or memory. Lines older than chats_egress_audit_days are pruned daily,
// with the audit of the Sandbox network (one frontend of a cluster, under an
// advisory lock).

// MaxEventsPage bounds one page of the audit.
const MaxEventsPage = 500

// maxFileEvents bounds the audit that the file store keeps.
const maxFileEvents = 10000

// Event is one command that ran on this machine.
type Event struct {
	User       string    `json:"user"`
	Chat       string    `json:"chat"`
	Command    string    `json:"command"`
	Exit       int       `json:"exit"` // -1: it did not exit by itself
	DurationMS int64     `json:"duration_ms"`
	TimedOut   bool      `json:"timed_out,omitempty"`
	At         time.Time `json:"at"`
}

// AuditStore keeps the audit of the commands.
type AuditStore interface {
	Add(ctx context.Context, ev Event) error
	// Events returns the newest lines, newest first.
	Events(ctx context.Context, limit int) ([]Event, error)
	// Prune removes the lines older than before and says how many.
	Prune(ctx context.Context, before time.Time) (int64, error)
}

type eventRow struct {
	Seq        int64  `gorm:"primaryKey;autoIncrement"`
	User       string `gorm:"size:255;index"`
	Chat       string `gorm:"size:64"`
	Command    string `gorm:"type:text"`
	Exit       int
	DurationMS int64
	TimedOut   bool
	At         time.Time `gorm:"index"`
}

func (eventRow) TableName() string { return "studio_host_exec_events" }

func (r eventRow) event() Event {
	return Event{User: r.User, Chat: r.Chat, Command: r.Command, Exit: r.Exit, DurationMS: r.DurationMS, TimedOut: r.TimedOut, At: r.At.UTC()}
}

type gormAudit struct{ db *gorm.DB }

// NewGORMAudit returns an AuditStore on db and creates its table.
func NewGORMAudit(db *gorm.DB) (AuditStore, error) {
	if db == nil {
		return nil, errors.New("host commands: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&eventRow{}); err != nil {
		return nil, fmt.Errorf("host commands: migrate the audit: %w", err)
	}
	return &gormAudit{db: db}, nil
}

func (g *gormAudit) Add(ctx context.Context, ev Event) error {
	r := eventRow{User: ev.User, Chat: ev.Chat, Command: ev.Command, Exit: ev.Exit, DurationMS: ev.DurationMS, TimedOut: ev.TimedOut, At: ev.At.UTC()}
	return g.db.WithContext(ctx).Create(&r).Error
}

func (g *gormAudit) Events(ctx context.Context, limit int) ([]Event, error) {
	var rows []eventRow
	if err := g.db.WithContext(ctx).Order("seq DESC").Limit(pageLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.event())
	}
	return out, nil
}

func (g *gormAudit) Prune(ctx context.Context, before time.Time) (int64, error) {
	res := g.db.WithContext(ctx).Where("at < ?", before.UTC()).Delete(&eventRow{})
	return res.RowsAffected, res.Error
}

type fileAudit struct {
	mu     sync.Mutex
	path   string // empty: memory only
	events []Event
}

// NewJSONAudit returns an AuditStore in the file audit.json of dir, or in
// memory when dir is empty. One process owns the file: a cluster uses the
// GORM store.
func NewJSONAudit(dir string) (AuditStore, error) {
	s := &fileAudit{}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("host commands: audit folder: %w", err)
	}
	s.path = filepath.Join(dir, "audit.json")
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("host commands: read the audit: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.events); err != nil {
			return nil, fmt.Errorf("host commands: read the audit: %w", err)
		}
	}
	return s, nil
}

func (s *fileAudit) Add(_ context.Context, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	if n := len(s.events) - maxFileEvents; n > 0 {
		s.events = slices.Clone(s.events[n:])
	}
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.events)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func (s *fileAudit) Prune(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.events[:0:0]
	for _, e := range s.events {
		if !e.At.Before(before) {
			keep = append(keep, e)
		}
	}
	n := int64(len(s.events) - len(keep))
	if n == 0 {
		return 0, nil
	}
	s.events = keep
	if s.path == "" {
		return n, nil
	}
	raw, err := json.Marshal(s.events)
	if err != nil {
		return n, err
	}
	return n, atomicfile.Write(s.path, raw, 0o600)
}

func (s *fileAudit) Events(_ context.Context, limit int) ([]Event, error) {
	limit = pageLimit(limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Event{}
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.events[i])
	}
	return out, nil
}

func pageLimit(limit int) int {
	if limit <= 0 || limit > MaxEventsPage {
		return MaxEventsPage
	}
	return limit
}
