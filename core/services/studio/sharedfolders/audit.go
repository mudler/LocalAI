package sharedfolders

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

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// The audit is kept beside the consents to shared pages: the auth database in
// a cluster, so every frontend writes the same audit, or a file under the data
// path, or memory.

// maxEventsPage bounds one page of the audit.
const maxEventsPage = 500

// maxFileEvents bounds the audit that the file store keeps.
const maxFileEvents = 10000

// AuditStore keeps the audit of the shared folders.
type AuditStore interface {
	Add(ctx context.Context, ev Event) error
	// Events returns the newest lines, newest first.
	Events(ctx context.Context, limit int) ([]Event, error)
}

type eventRow struct {
	Seq       int64  `gorm:"primaryKey;autoIncrement"`
	Actor     string `gorm:"size:255"`
	Action    string `gorm:"size:16"`
	FolderID  int64  `gorm:"index"`
	Folder    string `gorm:"size:1024"`
	Principal string `gorm:"size:255"`
	Mode      int
	Rev       int64
	At        time.Time
}

func (eventRow) TableName() string { return "studio_shared_folder_events" }

func (r eventRow) event() Event {
	return Event{Actor: r.Actor, Action: r.Action, FolderID: r.FolderID, Folder: r.Folder, Principal: r.Principal,
		Mode: workspace.Mode(r.Mode), Rev: r.Rev, At: r.At.UTC()}
}

type gormAudit struct{ db *gorm.DB }

// NewGORMAudit returns an AuditStore on db and creates its table.
func NewGORMAudit(db *gorm.DB) (AuditStore, error) {
	if db == nil {
		return nil, errors.New("shared folders: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&eventRow{}); err != nil {
		return nil, fmt.Errorf("shared folders: migrate the audit: %w", err)
	}
	return &gormAudit{db: db}, nil
}

func (g *gormAudit) Add(ctx context.Context, ev Event) error {
	r := eventRow{Actor: ev.Actor, Action: ev.Action, FolderID: ev.FolderID, Folder: ev.Folder, Principal: ev.Principal,
		Mode: int(ev.Mode), Rev: ev.Rev, At: ev.At.UTC()}
	return g.db.WithContext(ctx).Create(&r).Error
}

func (g *gormAudit) Events(ctx context.Context, limit int) ([]Event, error) {
	var rows []eventRow
	if err := g.db.WithContext(ctx).Order("seq DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.event())
	}
	return out, nil
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
		return nil, fmt.Errorf("shared folders: audit folder: %w", err)
	}
	s.path = filepath.Join(dir, "audit.json")
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("shared folders: read the audit: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.events); err != nil {
			return nil, fmt.Errorf("shared folders: read the audit: %w", err)
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

func (s *fileAudit) Events(_ context.Context, limit int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Event{}
	for i := len(s.events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.events[i])
	}
	return out, nil
}
