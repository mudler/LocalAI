// Package audit keeps the record of the network requests of the Sandbox:
// every request the proxy allowed or blocked, with who, which chat, the host,
// the verdict and, for an allowed one, the bytes it moved. It is kept like the
// audit of the commands on this machine: the auth database in a cluster, so
// every frontend writes and reads the same audit, a file under the data path
// in a standalone server, or memory without one.
package audit

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

// MaxPage bounds one page of the audit.
const MaxPage = 500

// maxFileEvents bounds the audit that the file store keeps.
const maxFileEvents = 20000

// Event is one network request of a command.
type Event struct {
	User       string    `json:"user"`
	Chat       string    `json:"chat"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Kind       string    `json:"kind"`    // connect or http
	Verdict    string    `json:"verdict"` // allow or block
	Reason     string    `json:"reason"`
	Addr       string    `json:"addr,omitempty"` // the address dialed
	BytesUp    int64     `json:"bytes_up,omitempty"`
	BytesDown  int64     `json:"bytes_down,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	At         time.Time `json:"at"`
}

// Query filters a page of the audit. Empty fields match everything.
type Query struct {
	Limit                     int
	User, Chat, Verdict, Host string
}

func (q Query) match(e Event) bool {
	return (q.User == "" || e.User == q.User) && (q.Chat == "" || e.Chat == q.Chat) &&
		(q.Verdict == "" || e.Verdict == q.Verdict) && (q.Host == "" || e.Host == q.Host)
}

// Counts are the totals of a period, by verdict.
type Counts struct {
	Allowed int64 `json:"allowed"`
	Blocked int64 `json:"blocked"`
}

// Store keeps the audit.
type Store interface {
	Add(ctx context.Context, evs ...Event) error
	// Events returns the newest lines that match q, newest first.
	Events(ctx context.Context, q Query) ([]Event, error)
	// Count totals the lines since a time.
	Count(ctx context.Context, since time.Time) (Counts, error)
	// Prune removes the lines older than before and says how many.
	Prune(ctx context.Context, before time.Time) (int64, error)
}

func pageLimit(n int) int {
	if n <= 0 || n > MaxPage {
		return MaxPage
	}
	return n
}

type eventRow struct {
	Seq        int64  `gorm:"primaryKey;autoIncrement"`
	User       string `gorm:"size:255;index"`
	Chat       string `gorm:"size:128;index"`
	Host       string `gorm:"size:255;index"`
	Port       int
	Kind       string `gorm:"size:16"`
	Verdict    string `gorm:"size:16"`
	Reason     string `gorm:"size:32"`
	Addr       string `gorm:"size:64"`
	BytesUp    int64
	BytesDown  int64
	DurationMS int64
	At         time.Time `gorm:"index"`
}

func (eventRow) TableName() string { return "studio_egress_events" }

func (r eventRow) event() Event {
	return Event{User: r.User, Chat: r.Chat, Host: r.Host, Port: r.Port, Kind: r.Kind, Verdict: r.Verdict, Reason: r.Reason,
		Addr: r.Addr, BytesUp: r.BytesUp, BytesDown: r.BytesDown, DurationMS: r.DurationMS, At: r.At.UTC()}
}

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db and creates its table.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("egress audit: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&eventRow{}); err != nil {
		return nil, fmt.Errorf("egress audit: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (g *gormStore) Add(ctx context.Context, evs ...Event) error {
	if len(evs) == 0 {
		return nil
	}
	rows := make([]eventRow, 0, len(evs))
	for _, e := range evs {
		rows = append(rows, eventRow{User: e.User, Chat: e.Chat, Host: e.Host, Port: e.Port, Kind: e.Kind, Verdict: e.Verdict, Reason: e.Reason,
			Addr: e.Addr, BytesUp: e.BytesUp, BytesDown: e.BytesDown, DurationMS: e.DurationMS, At: e.At.UTC()})
	}
	return g.db.WithContext(ctx).Create(&rows).Error
}

func (g *gormStore) Events(ctx context.Context, q Query) ([]Event, error) {
	// Struct conditions skip the empty fields and quote the column names
	// ("user" is a reserved word in PostgreSQL).
	tx := g.db.WithContext(ctx).Model(&eventRow{}).Where(&eventRow{User: q.User, Chat: q.Chat, Verdict: q.Verdict, Host: q.Host})
	var rows []eventRow
	if err := tx.Order("seq DESC").Limit(pageLimit(q.Limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.event())
	}
	return out, nil
}

func (g *gormStore) Count(ctx context.Context, since time.Time) (Counts, error) {
	var c Counts
	if err := g.db.WithContext(ctx).Model(&eventRow{}).Where("at >= ?", since.UTC()).Where(&eventRow{Verdict: "allow"}).Count(&c.Allowed).Error; err != nil {
		return c, err
	}
	err := g.db.WithContext(ctx).Model(&eventRow{}).Where("at >= ?", since.UTC()).Where(&eventRow{Verdict: "block"}).Count(&c.Blocked).Error
	return c, err
}

func (g *gormStore) Prune(ctx context.Context, before time.Time) (int64, error) {
	res := g.db.WithContext(ctx).Where("at < ?", before.UTC()).Delete(&eventRow{})
	return res.RowsAffected, res.Error
}

type fileStore struct {
	mu     sync.Mutex
	path   string // empty: memory only
	events []Event
}

// NewJSON returns a Store in the file audit.json of dir, or in memory when dir
// is empty. One process owns the file: a cluster uses the GORM store.
func NewJSON(dir string) (Store, error) {
	s := &fileStore{}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("egress audit: folder: %w", err)
	}
	s.path = filepath.Join(dir, "audit.json")
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("egress audit: read: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.events); err != nil {
			return nil, fmt.Errorf("egress audit: read: %w", err)
		}
	}
	return s, nil
}

func (s *fileStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.events)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func (s *fileStore) Add(_ context.Context, evs ...Event) error {
	if len(evs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, evs...)
	if n := len(s.events) - maxFileEvents; n > 0 {
		s.events = slices.Clone(s.events[n:])
	}
	return s.saveLocked()
}

func (s *fileStore) Events(_ context.Context, q Query) ([]Event, error) {
	limit := pageLimit(q.Limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Event{}
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		if q.match(s.events[i]) {
			out = append(out, s.events[i])
		}
	}
	return out, nil
}

func (s *fileStore) Count(_ context.Context, since time.Time) (Counts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c Counts
	for _, e := range s.events {
		if e.At.Before(since) {
			continue
		}
		switch e.Verdict {
		case "allow":
			c.Allowed++
		case "block":
			c.Blocked++
		}
	}
	return c, nil
}

func (s *fileStore) Prune(_ context.Context, before time.Time) (int64, error) {
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
	return n, s.saveLocked()
}
