package pages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// The runtime errors of a page: what its script threw (window.onerror and an
// unhandled rejection), caught by the bridge in the frame and reported by the
// UI of the owner. They exist so the assistant can read why a page does not
// work: it cannot run JavaScript, so without them nobody sees the error but
// the person at the screen. Nothing here goes to a chat on its own; the
// assistant reads them with the page_errors tool.
//
// The text of an error can hold data of the page, so the store keeps it
// per user and per page, never logs it, and keeps few of them.

// Limits of the errors of a page.
const (
	// MaxErrors is how many different errors a page keeps; the newest stay.
	MaxErrors = 10
	// MaxErrorMessage and MaxErrorStack cut the text of an error, in bytes.
	MaxErrorMessage = 500
	MaxErrorStack   = 1500
	// maxErrorPages bounds the pages with errors one user has.
	maxErrorPages = MaxPages
)

// ErrBadError means the report is empty or has no message.
var ErrBadError = errors.New("pages: an error report needs a message")

// ErrorReport is what the frame caught.
type ErrorReport struct {
	Message string
	Stack   string
	File    string // a script file of the page, "" for index.html
	Line    int    // 0 when unknown
	Column  int    // 0 when unknown
	Count   int    // how many times the frame saw it since its last report; 0 is 1
}

// PageError is one distinct error of a page, with how often it happened.
type PageError struct {
	Message string    `json:"message"`
	Stack   string    `json:"stack,omitempty"`
	File    string    `json:"file,omitempty"`
	Line    int       `json:"line,omitempty"`
	Column  int       `json:"column,omitempty"`
	Count   int       `json:"count"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
}

// ErrorSet is the errors of one page. Rev is the revision of the page's folder
// when they were caught: a page whose folder changed since has none, so the
// assistant never reads an error of code that is gone.
type ErrorSet struct {
	Rev    int64       `json:"rev"`
	Errors []PageError `json:"errors"`
}

// PageErrors names a page that has errors, for a listing.
type PageErrors struct {
	Page   string
	Errors int       // distinct errors
	Count  int       // all occurrences
	Last   time.Time // the newest
}

// ErrorStore keeps the error sets, one per user and page. A cluster uses the
// GORM store, so every frontend sees the errors that another recorded.
type ErrorStore interface {
	// Get returns the set of a page; the zero set when there is none.
	Get(ctx context.Context, principal, page string) (ErrorSet, error)
	// Update replaces the set of a page with what fn makes of the current one.
	// An empty result removes it.
	Update(ctx context.Context, principal, page string, fn func(ErrorSet) ErrorSet) error
	// All returns the sets of a user by page name.
	All(ctx context.Context, principal string) (map[string]ErrorSet, error)
}

// isNoise says that an error is the browser's own: it says nothing of the page's
// code, and the assistant can do nothing with it.
func isNoise(msg string) bool {
	return strings.HasPrefix(msg, "ResizeObserver loop") || msg == "Script error." || msg == "Script error"
}

// cut shortens s to max bytes on a rune boundary. keepNL keeps line breaks, for
// a stack; every other control character becomes a space.
func cut(s string, max int, keepNL bool) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' && keepNL {
			return r
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "")))
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// merge adds a report to a set. The same message at the same place is one
// error with a count; more than MaxErrors drop the oldest. A set of another
// revision starts again.
func merge(set ErrorSet, rev int64, r ErrorReport, now time.Time) ErrorSet {
	if set.Rev != rev {
		set = ErrorSet{Rev: rev}
	}
	n := max(r.Count, 1)
	for i, e := range set.Errors {
		if e.Message == r.Message && e.File == r.File && e.Line == r.Line && e.Column == r.Column {
			set.Errors[i].Count += n
			set.Errors[i].Last = now
			if r.Stack != "" {
				set.Errors[i].Stack = r.Stack
			}
			return set
		}
	}
	set.Errors = append(set.Errors, PageError{Message: r.Message, Stack: r.Stack, File: r.File, Line: r.Line, Column: r.Column, Count: n, First: now, Last: now})
	sort.SliceStable(set.Errors, func(i, j int) bool { return set.Errors[i].Last.After(set.Errors[j].Last) })
	if len(set.Errors) > MaxErrors {
		set.Errors = set.Errors[:MaxErrors]
	}
	return set
}

// clean checks and cuts a report. A browser's own noise is dropped (ok false).
func clean(r ErrorReport) (ErrorReport, bool, error) {
	r.Message = cut(r.Message, MaxErrorMessage, false)
	if r.Message == "" {
		return r, false, ErrBadError
	}
	if isNoise(r.Message) {
		return r, false, nil
	}
	r.Stack = cut(r.Stack, MaxErrorStack, true)
	r.File = cut(r.File, 100, false)
	if r.Line < 0 || r.Line > 1<<24 {
		r.Line = 0
	}
	if r.Column < 0 || r.Column > 1<<24 {
		r.Column = 0
	}
	r.Count = min(max(r.Count, 1), 1000)
	return r, true, nil
}

// revOf is the revision of the page's folder: it moves when any file of the
// page changes.
func (s *Service) revOf(ctx context.Context, l loc) (int64, error) {
	p, err := s.getAt(ctx, l)
	if err != nil {
		return 0, err
	}
	return p.Rev, nil
}

// RecordError keeps an error that the page's frame caught, for the owner's
// own page, and says whether it did: a noise error is dropped without a word.
// The text is never logged. A page that reports too fast gets ErrRateLimited.
func (s *Service) RecordError(ctx context.Context, principal, name string, r ErrorReport) (bool, error) {
	l, err := s.own(ctx, principal, name)
	if err != nil {
		return false, err
	}
	r, keep, err := clean(r)
	if err != nil || !keep {
		return false, err
	}
	rev, err := s.revOf(ctx, l)
	if err != nil {
		return false, err
	}
	if !s.errorRate.allow(l.key) {
		return false, ErrRateLimited
	}
	now := s.now().UTC()
	if err := s.errs.Update(ctx, principal, name, func(cur ErrorSet) ErrorSet { return merge(cur, rev, r, now) }); err != nil {
		return false, err
	}
	return true, nil
}

// Errors returns the recent errors of a page, newest first. A page whose files
// changed since they were caught has none.
func (s *Service) Errors(ctx context.Context, principal, name string) ([]PageError, error) {
	l, err := s.own(ctx, principal, name)
	if err != nil {
		return nil, err
	}
	rev, err := s.revOf(ctx, l)
	if err != nil {
		return nil, err
	}
	set, err := s.errs.Get(ctx, principal, name)
	if err != nil {
		return nil, err
	}
	if set.Rev != rev {
		if len(set.Errors) > 0 {
			// Stale: drop it, unless a newer set was stored meanwhile.
			_ = s.errs.Update(ctx, principal, name, func(cur ErrorSet) ErrorSet {
				if cur.Rev == set.Rev {
					return ErrorSet{}
				}
				return cur
			})
		}
		return []PageError{}, nil
	}
	return set.Errors, nil
}

// ErrorPages lists the pages of the user that have recent errors, the page with
// the newest error first.
func (s *Service) ErrorPages(ctx context.Context, principal string) ([]PageErrors, error) {
	all, err := s.errs.All(ctx, principal)
	if err != nil {
		return nil, err
	}
	out := []PageErrors{}
	for name, set := range all {
		if len(set.Errors) == 0 {
			continue
		}
		l, err := s.own(ctx, principal, name)
		if err != nil {
			continue // the page is gone
		}
		rev, err := s.revOf(ctx, l)
		if err != nil || rev != set.Rev {
			continue
		}
		pe := PageErrors{Page: name, Errors: len(set.Errors)}
		for _, e := range set.Errors {
			pe.Count += e.Count
			if e.Last.After(pe.Last) {
				pe.Last = e.Last
			}
		}
		out = append(out, pe)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Page < out[j].Page
	})
	return out, nil
}

// ---------- memory and file ----------

type fileErrors struct {
	mu   sync.Mutex
	path string // empty: memory only
	d    map[string]map[string]ErrorSet
}

// NewJSONErrors returns an ErrorStore in the file page-errors.json of dir, or in
// memory when dir is empty. One process owns the file: a cluster uses the GORM
// store.
func NewJSONErrors(dir string) (ErrorStore, error) {
	s := &fileErrors{d: map[string]map[string]ErrorSet{}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("pages: errors folder: %w", err)
	}
	s.path = filepath.Join(dir, "page-errors.json")
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("pages: read errors: %w", err)
	default:
		if err := json.Unmarshal(raw, &s.d); err != nil {
			return nil, fmt.Errorf("pages: read errors: %w", err)
		}
	}
	return s, nil
}

func (s *fileErrors) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func (s *fileErrors) Get(_ context.Context, principal, page string) (ErrorSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d[principal][page], nil
}

func (s *fileErrors) Update(_ context.Context, principal, page string, fn func(ErrorSet) ErrorSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := fn(s.d[principal][page])
	mine := s.d[principal]
	if len(next.Errors) == 0 {
		if _, ok := mine[page]; !ok {
			return nil
		}
		delete(mine, page)
		if len(mine) == 0 {
			delete(s.d, principal)
		}
		return s.save()
	}
	if mine == nil {
		mine = map[string]ErrorSet{}
		s.d[principal] = mine
	}
	if _, ok := mine[page]; !ok && len(mine) >= maxErrorPages {
		return nil
	}
	mine[page] = next
	return s.save()
}

func (s *fileErrors) All(_ context.Context, principal string) (map[string]ErrorSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]ErrorSet, len(s.d[principal]))
	for k, v := range s.d[principal] {
		out[k] = v
	}
	return out, nil
}

// ---------- GORM ----------

type errorRow struct {
	ID        int64  `gorm:"primaryKey;autoIncrement"`
	Principal string `gorm:"uniqueIndex:idx_page_errors,priority:1;size:255;not null"`
	Page      string `gorm:"uniqueIndex:idx_page_errors,priority:2;size:64;not null"`
	Data      string `gorm:"type:text;not null"`
	UpdatedAt time.Time
}

func (errorRow) TableName() string { return "studio_page_errors" }

type gormErrors struct{ db *gorm.DB }

// NewGORMErrors returns an ErrorStore on db and creates its table.
func NewGORMErrors(db *gorm.DB) (ErrorStore, error) {
	if db == nil {
		return nil, errors.New("pages: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&errorRow{}); err != nil {
		return nil, fmt.Errorf("pages: migrate errors: %w", err)
	}
	return &gormErrors{db: db}, nil
}

func decodeSet(data string) ErrorSet {
	var set ErrorSet
	if json.Unmarshal([]byte(data), &set) != nil {
		return ErrorSet{}
	}
	return set
}

func (g *gormErrors) Get(ctx context.Context, principal, page string) (ErrorSet, error) {
	var r errorRow
	err := g.db.WithContext(ctx).Where("principal = ? AND page = ?", principal, page).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrorSet{}, nil
	}
	if err != nil {
		return ErrorSet{}, err
	}
	return decodeSet(r.Data), nil
}

func (g *gormErrors) Update(ctx context.Context, principal, page string, fn func(ErrorSet) ErrorSet) error {
	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r errorRow
		err := tx.Where("principal = ? AND page = ?", principal, page).Take(&r).Error
		found := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		next := fn(decodeSet(r.Data))
		if len(next.Errors) == 0 {
			if !found {
				return nil
			}
			return tx.Delete(&r).Error
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if found {
			return tx.Model(&r).Updates(map[string]any{"data": string(raw), "updated_at": time.Now().UTC()}).Error
		}
		var n int64
		if err := tx.Model(&errorRow{}).Where("principal = ?", principal).Count(&n).Error; err != nil {
			return err
		}
		if n >= maxErrorPages {
			return nil
		}
		return tx.Create(&errorRow{Principal: principal, Page: page, Data: string(raw), UpdatedAt: time.Now().UTC()}).Error
	})
}

func (g *gormErrors) All(ctx context.Context, principal string) (map[string]ErrorSet, error) {
	var rows []errorRow
	if err := g.db.WithContext(ctx).Where("principal = ?", principal).Limit(maxErrorPages).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]ErrorSet, len(rows))
	for _, r := range rows {
		out[r.Page] = decodeSet(r.Data)
	}
	return out, nil
}
