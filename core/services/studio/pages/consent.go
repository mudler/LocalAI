package pages

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// A consent says that a viewer allowed one version of one shared page to run.
// It is kept on the server, not in the viewer's files: everything that acts as
// the viewer in the workspace (the assistant's file tools, a page's bridge, a
// trigger run, an add-on) can write the viewer's files, so a consent kept there
// could be written by the very code it is meant to hold back. The store is the
// auth database in a cluster, so every frontend sees the same consents.
//
// A consent names the page by the node ID of its folder and the version by
// the hash of its code, so a consent for one page never lets another page run,
// even one with the same code, and a changed page asks again.

// Consent kinds.
const (
	// ConsentAlways runs the version each time the viewer opens it.
	ConsentAlways = "always"
	// ConsentOnce runs it for one visit: the visit's token is needed by every
	// call of the bridge, and the consent ends with OnceTTL.
	ConsentOnce = "once"
)

// OnceTTL bounds a visit that the viewer allowed once.
const OnceTTL = 12 * time.Hour

// ErrNoConsent means the viewer has not allowed this version of the page.
var ErrNoConsent = errors.New("pages: you have not allowed this version of the shared page")

// Consent is a viewer's consent to one version of a shared page.
type Consent struct {
	Principal string
	PageID    int64  // node ID of the page's folder
	Folder    string // the shared folder, for the audit
	Name      string
	Hash      string
	Kind      string // ConsentAlways or ConsentOnce
	Token     string // ConsentOnce only
	// The size of index.html and page.json and the lines of index.html of the
	// version allowed, so a later sheet can say what changed.
	Size, MetaSize int64
	Lines          int
	At             time.Time
	Expires        time.Time // ConsentOnce only
}

// ConsentEvent is one line of the audit of consents.
type ConsentEvent struct {
	Principal string
	PageID    int64
	Folder    string
	Name      string
	Hash      string
	Action    string // allow, allow_once, revoke
	At        time.Time
}

// ConsentStore keeps the consents and their audit.
type ConsentStore interface {
	// Always returns the viewer's lasting consent for a page, if any.
	Always(ctx context.Context, principal string, pageID int64) (Consent, bool, error)
	// Once returns the visit consent with token.
	Once(ctx context.Context, principal, token string) (Consent, bool, error)
	// Put stores a consent. A lasting one replaces the viewer's lasting one for
	// the page; a visit one is added. ev is written in the same step.
	Put(ctx context.Context, c Consent, ev ConsentEvent) error
	// Revoke removes every consent of the viewer for the page and writes ev.
	Revoke(ctx context.Context, principal string, pageID int64, ev ConsentEvent) error
	// Events returns the newest audit lines of a viewer, newest first; an empty
	// principal returns everyone's.
	Events(ctx context.Context, principal string, limit int) ([]ConsentEvent, error)
}

// NewToken returns a random token for a visit.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- GORM ----------

type consentRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Principal string `gorm:"index:idx_page_consent,priority:1;size:255;not null"`
	PageID    int64  `gorm:"index:idx_page_consent,priority:2;not null"`
	Kind      string `gorm:"size:16;not null"`
	Token     string `gorm:"index;size:64"`
	Folder    string `gorm:"size:1024"`
	Name      string `gorm:"size:64"`
	Hash      string `gorm:"size:64"`
	Size      int64
	MetaSize  int64
	Lines     int
	At        time.Time
	Expires   time.Time
}

func (consentRow) TableName() string { return "studio_page_consents" }

type consentEventRow struct {
	Seq       int64  `gorm:"primaryKey;autoIncrement"`
	Principal string `gorm:"index;size:255"`
	PageID    int64
	Folder    string `gorm:"size:1024"`
	Name      string `gorm:"size:64"`
	Hash      string `gorm:"size:64"`
	Action    string `gorm:"size:16"`
	At        time.Time
}

func (consentEventRow) TableName() string { return "studio_page_consent_events" }

type gormConsents struct{ db *gorm.DB }

// NewGORMConsents returns a ConsentStore on db and creates its tables.
func NewGORMConsents(db *gorm.DB) (ConsentStore, error) {
	if db == nil {
		return nil, errors.New("pages: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&consentRow{}, &consentEventRow{}); err != nil {
		return nil, fmt.Errorf("pages: migrate consents: %w", err)
	}
	return &gormConsents{db: db}, nil
}

func (r consentRow) consent() Consent {
	return Consent{Principal: r.Principal, PageID: r.PageID, Folder: r.Folder, Name: r.Name, Hash: r.Hash, Kind: r.Kind, Token: r.Token,
		Size: r.Size, MetaSize: r.MetaSize, Lines: r.Lines, At: r.At.UTC(), Expires: r.Expires.UTC()}
}

func rowOf(c Consent) consentRow {
	return consentRow{Principal: c.Principal, PageID: c.PageID, Kind: c.Kind, Token: c.Token, Folder: c.Folder, Name: c.Name, Hash: c.Hash,
		Size: c.Size, MetaSize: c.MetaSize, Lines: c.Lines, At: c.At.UTC(), Expires: c.Expires.UTC()}
}

func eventRow(e ConsentEvent) consentEventRow {
	return consentEventRow{Principal: e.Principal, PageID: e.PageID, Folder: e.Folder, Name: e.Name, Hash: e.Hash, Action: e.Action, At: e.At.UTC()}
}

func (g *gormConsents) Always(ctx context.Context, principal string, pageID int64) (Consent, bool, error) {
	var r consentRow
	err := g.db.WithContext(ctx).Where("principal = ? AND page_id = ? AND kind = ?", principal, pageID, ConsentAlways).Order("id DESC").Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Consent{}, false, nil
	}
	if err != nil {
		return Consent{}, false, err
	}
	return r.consent(), true, nil
}

func (g *gormConsents) Once(ctx context.Context, principal, token string) (Consent, bool, error) {
	if token == "" {
		return Consent{}, false, nil
	}
	var r consentRow
	err := g.db.WithContext(ctx).Where("principal = ? AND token = ? AND kind = ?", principal, token, ConsentOnce).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Consent{}, false, nil
	}
	if err != nil {
		return Consent{}, false, err
	}
	return r.consent(), true, nil
}

func (g *gormConsents) Put(ctx context.Context, c Consent, ev ConsentEvent) error {
	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Visits that ended are dropped as new ones come.
		if err := tx.Where("kind = ? AND expires < ?", ConsentOnce, time.Now().UTC()).Delete(&consentRow{}).Error; err != nil {
			return err
		}
		if c.Kind == ConsentAlways {
			if err := tx.Where("principal = ? AND page_id = ? AND kind = ?", c.Principal, c.PageID, ConsentAlways).Delete(&consentRow{}).Error; err != nil {
				return err
			}
		}
		row := rowOf(c)
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		er := eventRow(ev)
		return tx.Create(&er).Error
	})
}

func (g *gormConsents) Revoke(ctx context.Context, principal string, pageID int64, ev ConsentEvent) error {
	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("principal = ? AND page_id = ?", principal, pageID).Delete(&consentRow{}).Error; err != nil {
			return err
		}
		er := eventRow(ev)
		return tx.Create(&er).Error
	})
}

func (g *gormConsents) Events(ctx context.Context, principal string, limit int) ([]ConsentEvent, error) {
	q := g.db.WithContext(ctx).Order("seq DESC").Limit(limit)
	if principal != "" {
		q = q.Where("principal = ?", principal)
	}
	var rows []consentEventRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ConsentEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, ConsentEvent{Principal: r.Principal, PageID: r.PageID, Folder: r.Folder, Name: r.Name, Hash: r.Hash, Action: r.Action, At: r.At.UTC()})
	}
	return out, nil
}

// ---------- JSON file, or memory ----------

// maxConsentEvents bounds the audit that the file store keeps.
const maxConsentEvents = 10000

type fileConsents struct {
	mu   sync.Mutex
	path string // empty: memory only
	d    struct {
		Consents []Consent      `json:"consents"`
		Events   []ConsentEvent `json:"events"`
	}
	now func() time.Time
}

// NewJSONConsents returns a ConsentStore in the file consents.json of dir, or
// in memory when dir is empty. One process owns the file: a cluster uses the
// GORM store.
func NewJSONConsents(dir string) (ConsentStore, error) {
	s := &fileConsents{now: time.Now}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("pages: consents folder: %w", err)
	}
	s.path = filepath.Join(dir, "consents.json")
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("pages: read consents: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.d); err != nil {
			return nil, fmt.Errorf("pages: read consents: %w", err)
		}
	}
	return s, nil
}

func (s *fileConsents) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(&s.d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func (s *fileConsents) Always(_ context.Context, principal string, pageID int64) (Consent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.d.Consents {
		if c.Principal == principal && c.PageID == pageID && c.Kind == ConsentAlways {
			return c, true, nil
		}
	}
	return Consent{}, false, nil
}

func (s *fileConsents) Once(_ context.Context, principal, token string) (Consent, bool, error) {
	if token == "" {
		return Consent{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.d.Consents {
		if c.Principal == principal && c.Kind == ConsentOnce && c.Token == token {
			return c, true, nil
		}
	}
	return Consent{}, false, nil
}

func (s *fileConsents) log(ev ConsentEvent) {
	s.d.Events = append(s.d.Events, ev)
	if n := len(s.d.Events) - maxConsentEvents; n > 0 {
		s.d.Events = slices.Clone(s.d.Events[n:])
	}
}

func (s *fileConsents) Put(_ context.Context, c Consent, ev ConsentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	keep := s.d.Consents[:0:0]
	for _, x := range s.d.Consents {
		if x.Kind == ConsentOnce && x.Expires.Before(now) {
			continue
		}
		if c.Kind == ConsentAlways && x.Kind == ConsentAlways && x.Principal == c.Principal && x.PageID == c.PageID {
			continue
		}
		keep = append(keep, x)
	}
	s.d.Consents = append(keep, c)
	s.log(ev)
	return s.save()
}

func (s *fileConsents) Revoke(_ context.Context, principal string, pageID int64, ev ConsentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Consents = slices.DeleteFunc(s.d.Consents, func(x Consent) bool { return x.Principal == principal && x.PageID == pageID })
	s.log(ev)
	return s.save()
}

func (s *fileConsents) Events(_ context.Context, principal string, limit int) ([]ConsentEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ConsentEvent{}
	for i := len(s.d.Events) - 1; i >= 0 && len(out) < limit; i-- {
		if principal == "" || s.d.Events[i].Principal == principal {
			out = append(out, s.d.Events[i])
		}
	}
	return out, nil
}
