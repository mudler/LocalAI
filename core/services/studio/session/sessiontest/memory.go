package sessiontest

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// memoryStore is a session.Store that keeps everything in process. It is for
// tests of the loop; the product stores live in the studio store package.
type memoryStore struct {
	mu      sync.Mutex
	chats   map[string]session.ChatRecord
	entries map[string][]session.Entry
}

// NewMemoryStore returns an empty in-memory session.Store.
func NewMemoryStore() session.Store {
	return &memoryStore{chats: map[string]session.ChatRecord{}, entries: map[string][]session.Entry{}}
}

func (s *memoryStore) CreateChat(_ context.Context, c session.ChatRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.chats[c.ID]; ok {
		return session.ErrExists
	}
	s.chats[c.ID] = c
	return nil
}

func (s *memoryStore) GetChat(_ context.Context, principal, chatID string) (session.ChatRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[chatID]
	if !ok || c.Principal != principal {
		return session.ChatRecord{}, session.ErrNotFound
	}
	return c, nil
}

func (s *memoryStore) ListChats(_ context.Context, principal string, before session.ChatCursor, limit int) ([]session.ChatRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []session.ChatRecord
	for _, c := range s.chats {
		if c.Principal != principal {
			continue
		}
		if !before.Updated.IsZero() && !(c.Updated.Before(before.Updated) || (c.Updated.Equal(before.Updated) && c.ID < before.ID)) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memoryStore) UpdateChat(_ context.Context, c session.ChatRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.chats[c.ID]
	if !ok || old.Principal != c.Principal {
		return session.ErrNotFound
	}
	old.Title, old.Folder, old.Snapshot, old.HarnessState, old.Turns, old.Updated = c.Title, c.Folder, c.Snapshot, c.HarnessState, c.Turns, c.Updated
	s.chats[c.ID] = old
	return nil
}

func (s *memoryStore) DeleteChat(_ context.Context, principal, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[chatID]
	if !ok || c.Principal != principal {
		return session.ErrNotFound
	}
	delete(s.chats, chatID)
	delete(s.entries, chatID)
	return nil
}

func (s *memoryStore) AppendEntry(_ context.Context, chatID string, e session.Entry) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.chats[chatID]; !ok {
		return 0, session.ErrNotFound
	}
	e.Seq = int64(len(s.entries[chatID]) + 1)
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	s.entries[chatID] = append(s.entries[chatID], e)
	return e.Seq, nil
}

func (s *memoryStore) AppendEntries(_ context.Context, chatID string, entries []session.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.chats[chatID]; !ok {
		return session.ErrNotFound
	}
	for _, e := range entries {
		e.Seq = int64(len(s.entries[chatID]) + 1)
		if e.At.IsZero() {
			e.At = time.Now().UTC()
		}
		s.entries[chatID] = append(s.entries[chatID], e)
	}
	return nil
}

func (s *memoryStore) Entries(_ context.Context, chatID string, after int64, limit int) ([]session.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []session.Entry
	for _, e := range s.entries[chatID] {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
