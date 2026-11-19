package notify

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

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonStore keeps the notifications in memory and, with a directory, in the file
// notifications.json (rewritten atomically after every change). One process owns
// the directory; a cluster uses the GORM store.
type jsonStore struct {
	mu   sync.Mutex
	path string // empty: memory only
	st   jsonFile
}

type jsonFile struct {
	Next int64     `json:"next"`
	Rows []jsonRow `json:"rows"`
}

type jsonRow struct {
	ID        int64          `json:"id"`
	Principal string         `json:"principal"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status"`
	Source    string         `json:"source,omitempty"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Link      string         `json:"link"`
	Read      bool           `json:"read"`
	Created   time.Time      `json:"created"`
	Code      string         `json:"code,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	Key       string         `json:"key,omitempty"`
	Ids       []string       `json:"ids,omitempty"`
	Hidden    bool           `json:"hidden,omitempty"`
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "notifications.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.st); err != nil {
		return nil, fmt.Errorf("notify: read %s: %w", s.path, err)
	}
	return s, nil
}

func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	return atomicfile.WriteJSON(s.path, s.st, 0o600)
}

// change runs f on a copy of the state and keeps the copy only when it saved.
func (s *jsonStore) change(f func(*jsonFile)) error {
	next := jsonFile{Next: s.st.Next, Rows: slices.Clone(s.st.Rows)}
	f(&next)
	old := s.st
	s.st = next
	if err := s.save(); err != nil {
		s.st = old
		return err
	}
	return nil
}

func (r jsonRow) sticky() bool { return r.Key != "" && len(r.Ids) > 0 }

// trim keeps the newest MaxPerUser rows of the principal that are not past
// MaxAge counted back from at. Set rows stay. Rows are in id order.
func (f *jsonFile) trim(principal string, at time.Time) {
	cutoff := at.Add(-MaxAge)
	kept := 0
	keep := make([]bool, len(f.Rows))
	for i := len(f.Rows) - 1; i >= 0; i-- {
		r := f.Rows[i]
		if r.Principal != principal || r.sticky() {
			keep[i] = true
			continue
		}
		if kept < MaxPerUser && !r.Created.Before(cutoff) {
			keep[i] = true
			kept++
		}
	}
	rows := f.Rows[:0:0]
	for i, r := range f.Rows {
		if keep[i] {
			rows = append(rows, r)
		}
	}
	f.Rows = rows
}

func newJSONRow(id int64, n Notification) jsonRow {
	return jsonRow{ID: id, Principal: n.Principal, Kind: n.Kind, Status: n.Status, Source: n.Source,
		Title: n.Title, Body: n.Body, Link: n.Link, Created: n.Created, Code: n.Code, Params: n.Params,
		Key: n.Key, Ids: slices.Clone(n.Ids)}
}

func (s *jsonStore) Add(_ context.Context, n Notification) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n.Key, n.Ids = "", nil
	var id int64
	err := s.change(func(f *jsonFile) {
		f.Next++
		id = f.Next
		f.Rows = append(f.Rows, newJSONRow(id, n))
		f.trim(n.Principal, n.Created)
	})
	return id, err
}

func (s *jsonStore) Upsert(_ context.Context, n Notification, mode UpsertMode) (Notification, UpsertResult, error) {
	if n.Key == "" {
		return Notification{}, "", errors.New("notify: upsert needs a key")
	}
	if mode == ModeOnce {
		n.Ids = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at := slices.IndexFunc(s.st.Rows, func(r jsonRow) bool { return r.Principal == n.Principal && r.Key == n.Key })
	var old *Notification
	if at >= 0 {
		o := toNotification(s.st.Rows[at])
		old = &o
	}
	res := Decide(old, n, mode)
	var out Notification
	err := s.change(func(f *jsonFile) {
		switch res {
		case ResultUnchanged:
			if old != nil {
				out = *old
			}
		case ResultRemoved:
			out = *old
			f.Rows = slices.Delete(f.Rows, at, at+1)
		case ResultUpdated:
			r := &f.Rows[at]
			r.Kind, r.Status, r.Source, r.Title, r.Body, r.Link = n.Kind, n.Status, n.Source, n.Title, n.Body, n.Link
			r.Code, r.Params, r.Ids = n.Code, n.Params, slices.Clone(n.Ids)
			out = toNotification(*r)
		default:
			// A revived item is a new row, so it sorts first and clients that
			// watch for ids above the highest one seen announce it.
			if res == ResultRevived {
				f.Rows = slices.Delete(f.Rows, at, at+1)
			}
			f.Next++
			f.Rows = append(f.Rows, newJSONRow(f.Next, n))
			out = toNotification(f.Rows[len(f.Rows)-1])
			f.trim(n.Principal, n.Created)
		}
	})
	if err != nil {
		return Notification{}, "", err
	}
	return out, res, nil
}

func toNotification(r jsonRow) Notification {
	return Notification{ID: r.ID, Principal: r.Principal, Kind: r.Kind, Status: r.Status, Source: r.Source, Title: r.Title,
		Body: r.Body, Link: r.Link, Read: r.Read, Created: r.Created, Code: r.Code, Params: r.Params, Key: r.Key,
		Ids: slices.Clone(r.Ids), Hidden: r.Hidden}
}

func (s *jsonStore) List(_ context.Context, principal string, unreadOnly bool, limit int) ([]Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Notification
	for _, r := range slices.Backward(s.st.Rows) {

		if r.Principal != principal || r.Hidden || (unreadOnly && r.Read) {
			continue
		}
		out = append(out, toNotification(r))
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

func (s *jsonStore) Count(_ context.Context, principal string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.st.Rows {
		if r.Principal == principal && !r.Read && !r.Hidden {
			n++
		}
	}
	return n, nil
}

func (s *jsonStore) mark(principal string, match func(jsonRow) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(func(f *jsonFile) {
		for i := range f.Rows {
			if f.Rows[i].Principal == principal && match(f.Rows[i]) {
				f.Rows[i].Read = true
			}
		}
	})
}

func (s *jsonStore) MarkRead(_ context.Context, principal string, ids []int64) error {
	return s.mark(principal, func(r jsonRow) bool { return slices.Contains(ids, r.ID) })
}

func (s *jsonStore) MarkChatRead(_ context.Context, principal, link string) error {
	return s.mark(principal, func(r jsonRow) bool { return r.Link == link })
}

func (s *jsonStore) MarkAllRead(_ context.Context, principal string) error {
	return s.mark(principal, func(jsonRow) bool { return true })
}

// remove drops the plain rows of the principal that match and hides the keyed
// ones. It returns how many visible rows went.
func (s *jsonStore) remove(principal string, match func(jsonRow) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	err := s.change(func(f *jsonFile) {
		f.Rows = slices.DeleteFunc(f.Rows, func(r jsonRow) bool {
			if r.Principal != principal || !match(r) {
				return false
			}
			if r.Key != "" {
				return false
			}
			removed++
			return true
		})
		for i := range f.Rows {
			r := &f.Rows[i]
			if r.Principal == principal && r.Key != "" && !r.Hidden && match(*r) {
				r.Hidden, r.Read = true, true
				removed++
			}
		}
	})
	return removed, err
}

func (s *jsonStore) Delete(_ context.Context, principal string, ids []int64) error {
	_, err := s.remove(principal, func(r jsonRow) bool { return slices.Contains(ids, r.ID) })
	return err
}

func (s *jsonStore) DeleteAll(_ context.Context, principal string, readOnly bool) (int, error) {
	return s.remove(principal, func(r jsonRow) bool { return !readOnly || r.Read })
}
