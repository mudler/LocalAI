package notify

import (
	"context"
	"encoding/json"
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
	ID        int64     `json:"id"`
	Principal string    `json:"principal"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Source    string    `json:"source,omitempty"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Link      string    `json:"link"`
	Read      bool      `json:"read"`
	Created   time.Time `json:"created"`
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

func (s *jsonStore) Add(_ context.Context, n Notification) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.change(func(f *jsonFile) {
		f.Next++
		id = f.Next
		f.Rows = append(f.Rows, jsonRow{ID: id, Principal: n.Principal, Kind: n.Kind, Status: n.Status, Source: n.Source,
			Title: n.Title, Body: n.Body, Link: n.Link, Created: n.Created})
		cutoff := n.Created.Add(-MaxAge)
		kept := 0
		// Rows are in id order; walk from the newest so the newest MaxPerUser stay.
		keep := make([]bool, len(f.Rows))
		for i := len(f.Rows) - 1; i >= 0; i-- {
			r := f.Rows[i]
			if r.Principal != n.Principal {
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
	})
	return id, err
}

func toNotification(r jsonRow) Notification {
	return Notification{ID: r.ID, Principal: r.Principal, Kind: r.Kind, Status: r.Status, Source: r.Source, Title: r.Title,
		Body: r.Body, Link: r.Link, Read: r.Read, Created: r.Created}
}

func (s *jsonStore) List(_ context.Context, principal string, unreadOnly bool, limit int) ([]Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Notification
	for _, r := range slices.Backward(s.st.Rows) {

		if r.Principal != principal || (unreadOnly && r.Read) {
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
		if r.Principal == principal && !r.Read {
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

func (s *jsonStore) Delete(_ context.Context, principal string, ids []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(func(f *jsonFile) {
		f.Rows = slices.DeleteFunc(f.Rows, func(r jsonRow) bool { return r.Principal == principal && slices.Contains(ids, r.ID) })
	})
}

func (s *jsonStore) DeleteAll(_ context.Context, principal string, readOnly bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	err := s.change(func(f *jsonFile) {
		f.Rows = slices.DeleteFunc(f.Rows, func(r jsonRow) bool {
			gone := r.Principal == principal && (!readOnly || r.Read)
			if gone {
				removed++
			}
			return gone
		})
	})
	return removed, err
}
