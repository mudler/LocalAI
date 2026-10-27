package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// jsonStore keeps everything in memory and, with a directory, in connectors.json
// (mode 0600, rewritten atomically). The sealing key is the file connectors.key
// (mode 0600): a key file that others can read is refused. One process owns the
// directory; a cluster uses the GORM store.
type jsonStore struct {
	mu      sync.Mutex
	path    string
	keyPath string
	key     []byte
	d       jsonData
}

type jsonData struct {
	Bindings map[string]Binding  `json:"bindings"`
	Offsets  map[string]int64    `json:"offsets"`
	Senders  map[string][]Sender `json:"senders"`
	Chats    map[string]string   `json:"chats"` // binding + "\x00" + sender
}

func newData() jsonData {
	return jsonData{Bindings: map[string]Binding{}, Offsets: map[string]int64{}, Senders: map[string][]Sender{}, Chats: map[string]string{}}
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{d: newData()}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path, s.keyPath = filepath.Join(dir, "connectors.json"), filepath.Join(dir, "connectors.key")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.d); err != nil {
		return nil, fmt.Errorf("connectors: read %s: %w", s.path, err)
	}
	if s.d.Bindings == nil {
		s.d = newData()
	}
	return s, nil
}

func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.d)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *jsonStore) PutBinding(_ context.Context, b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, had := s.d.Bindings[b.ID]
	s.d.Bindings[b.ID] = b.clone()
	if err := s.save(); err != nil {
		if had {
			s.d.Bindings[b.ID] = old
		} else {
			delete(s.d.Bindings, b.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) GetBinding(_ context.Context, id string) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.d.Bindings[id]
	if !ok {
		return Binding{}, ErrNotFound
	}
	return Normalize(b.clone()), nil
}

func (s *jsonStore) list(keep func(Binding) bool) []Binding {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Binding
	for _, b := range s.d.Bindings {
		if keep(b) {
			out = append(out, Normalize(b.clone()))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *jsonStore) ListBindings(_ context.Context, owner string) ([]Binding, error) {
	return s.list(func(b Binding) bool { return b.Owner == owner }), nil
}

func (s *jsonStore) EnabledBindings(context.Context) ([]Binding, error) {
	return s.list(func(b Binding) bool { return b.Enabled }), nil
}

func (s *jsonStore) DeleteBinding(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.d.Bindings, id)
	delete(s.d.Offsets, id)
	delete(s.d.Senders, id)
	for k := range s.d.Chats {
		if len(k) > len(id) && k[:len(id)+1] == id+"\x00" {
			delete(s.d.Chats, k)
		}
	}
	return s.save()
}

func (s *jsonStore) Offset(_ context.Context, id string) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	off, ok := s.d.Offsets[id]
	return off, ok, nil
}

func (s *jsonStore) SetOffset(_ context.Context, id string, offset int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Offsets[id] = offset
	return s.save()
}

func (s *jsonStore) RecordSender(_ context.Context, binding, senderID, name string, allowed bool, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.d.Senders[binding]
	found := false
	for i := range list {
		if list[i].ID == senderID {
			list[i].Count++
			list[i].Name, list[i].Last, list[i].Allowed = name, at.UTC(), allowed
			found = true
		}
	}
	if !found {
		list = append(list, Sender{Binding: binding, ID: senderID, Name: name, Count: 1, First: at.UTC(), Last: at.UTC(), Allowed: allowed})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Last.After(list[j].Last) })
	if len(list) > MaxSendersKept {
		list = list[:MaxSendersKept]
	}
	s.d.Senders[binding] = list
	return s.save()
}

func (s *jsonStore) Senders(_ context.Context, binding string, limit int) ([]Sender, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := append([]Sender(nil), s.d.Senders[binding]...)
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (s *jsonStore) ChatOf(_ context.Context, binding, senderID string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.d.Chats[binding+"\x00"+senderID]
	return id, ok, nil
}

func (s *jsonStore) SetChat(_ context.Context, binding, senderID, chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Chats[binding+"\x00"+senderID] = chatID
	return s.save()
}

func (s *jsonStore) EnsureKey(_ context.Context, generate func() ([]byte, error)) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != nil {
		return append([]byte(nil), s.key...), nil
	}
	if s.keyPath != "" {
		st, err := os.Stat(s.keyPath)
		switch {
		case err == nil:
			if st.Mode().Perm()&0o077 != 0 {
				return nil, errors.New("connectors: the key file can be read by others; run chmod 600 on it")
			}
			k, err := os.ReadFile(s.keyPath)
			if err != nil || len(k) != KeySize {
				return nil, fmt.Errorf("connectors: the key file is not a 32-byte key (%v)", err)
			}
			s.key = k
			return append([]byte(nil), k...), nil
		case !os.IsNotExist(err):
			return nil, err
		}
	}
	k, err := generate()
	if err != nil {
		return nil, err
	}
	if s.keyPath != "" {
		if err := os.WriteFile(s.keyPath, k, 0o600); err != nil {
			return nil, err
		}
	}
	s.key = k
	return append([]byte(nil), k...), nil
}
