package remotes

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

	"github.com/mudler/LocalAI/pkg/atomicfile"
	"github.com/mudler/LocalAI/pkg/fsperm"
	"github.com/mudler/xlog"
)

const (
	jsonFileName = "remotes.json"
	jsonKeyName  = "remotes.key"
)

// jsonStore keeps the records in one file (mode 0600, rewritten atomically)
// and the sealing key in a second file of the same mode. It reads the file on
// every call and keeps nothing in memory between calls. One process owns the
// directory; a cluster uses the GORM store.
type jsonStore struct {
	mu      sync.Mutex
	path    string
	keyPath string
	// mem is used when there is no directory (tests, embedding).
	mem map[string]record
	key []byte
	now func() time.Time
}

type jsonFile struct {
	Version int               `json:"version"`
	Remotes map[string]record `json:"remotes"`
}

// NewJSONStore returns a Store that keeps its data in dir. With an empty dir
// it keeps everything in memory.
func NewJSONStore(dir string) (Store, error) {
	s := &jsonStore{now: time.Now}
	if dir == "" {
		s.mem = map[string]record{}
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("remotes: create store directory: %w", err)
	}
	// MkdirAll keeps the mode of a directory that exists. The directory
	// holds the sealing key, so it is closed to others whatever made it.
	if fsperm.Check(dir) != nil {
		if err := fsperm.Restrict(dir); err != nil {
			return nil, fmt.Errorf("remotes: other users can open the store directory and it could not be made private: %w", err)
		}
		xlog.Warn("Other users could open the remotes store directory; it is now private", "dir", dir)
	}
	s.path = filepath.Join(dir, jsonFileName)
	s.keyPath = filepath.Join(dir, jsonKeyName)
	return s, nil
}

// load reads the file. A file that does not parse is moved aside, so the
// sealed keys in it are not lost and the service keeps working with an empty
// list; the move is logged at error level.
func (s *jsonStore) load() (map[string]record, error) {
	if s.path == "" {
		return s.mem, nil
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]record{}, nil
	}
	if err == nil {
		s.fixFileMode()
	}
	if err != nil {
		return nil, fmt.Errorf("remotes: read store: %w", err)
	}
	var f jsonFile
	if err := json.Unmarshal(raw, &f); err != nil {
		aside := fmt.Sprintf("%s.corrupt-%d", s.path, s.now().Unix())
		if rerr := os.Rename(s.path, aside); rerr != nil {
			return nil, fmt.Errorf("remotes: the store file is damaged and could not be moved aside: %w", rerr)
		}
		xlog.Error("The remotes file is damaged; it was moved aside and the list starts empty", "moved_to", aside)
		return map[string]record{}, nil
	}
	if f.Remotes == nil {
		f.Remotes = map[string]record{}
	}
	return f.Remotes, nil
}

// fixFileMode closes the store file to others when an outside tool loosened
// its mode. The sealed values are useless without the key, but the file is
// still not for others to read.
func (s *jsonStore) fixFileMode() {
	if fsperm.Check(s.path) == nil {
		return
	}
	if err := fsperm.Restrict(s.path); err != nil {
		xlog.Warn("Other users can open the remotes file and it could not be made private", "path", s.path, "error", err)
		return
	}
	xlog.Warn("Other users could open the remotes file; it is now private", "path", s.path)
}

func (s *jsonStore) save(m map[string]record) error {
	if s.path == "" {
		s.mem = m
		return nil
	}
	return atomicfile.WriteJSON(s.path, jsonFile{Version: 1, Remotes: m}, 0o600)
}

func nameTaken(m map[string]record, name, exceptID string) bool {
	k := nameKey(name)
	for id, r := range m {
		if id != exceptID && nameKey(r.Name) == k {
			return true
		}
	}
	return false
}

func (s *jsonStore) Create(_ context.Context, r record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := m[r.ID]; ok || nameTaken(m, r.Name, "") {
		return ErrConflict
	}
	m[r.ID] = r.clone()
	return s.save(m)
}

func (s *jsonStore) Get(_ context.Context, id string) (record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return record{}, err
	}
	r, ok := m[id]
	if !ok {
		return record{}, ErrNotFound
	}
	return r.clone(), nil
}

func (s *jsonStore) List(_ context.Context) ([]record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]record, 0, len(m))
	for _, r := range m {
		out = append(out, r.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := nameKey(out[i].Name), nameKey(out[j].Name); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *jsonStore) Update(_ context.Context, id string, fn func(*record) error) (record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return record{}, err
	}
	cur, ok := m[id]
	if !ok {
		return record{}, ErrNotFound
	}
	next := cur.clone()
	if err := fn(&next); err != nil {
		return record{}, err
	}
	next.ID = id
	if nameTaken(m, next.Name, id) {
		return record{}, ErrConflict
	}
	m[id] = next.clone()
	if err := s.save(m); err != nil {
		return record{}, err
	}
	return next, nil
}

func (s *jsonStore) Delete(_ context.Context, id string, check func(record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	cur, ok := m[id]
	if !ok {
		return ErrNotFound
	}
	if check != nil {
		if err := check(cur.clone()); err != nil {
			return err
		}
	}
	delete(m, id)
	return s.save(m)
}

// EnsureKey returns the sealing key. The key file must not be readable by
// others: a file that is gets refused instead of used.
func (s *jsonStore) EnsureKey(_ context.Context, generate func() ([]byte, error)) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keyPath == "" {
		if s.key == nil {
			if anySealed(s.mem) {
				return nil, errKeyMissing
			}
			k, err := generate()
			if err != nil {
				return nil, err
			}
			s.key = k
		}
		return append([]byte(nil), s.key...), nil
	}
	_, err := os.Stat(s.keyPath)
	switch {
	case err == nil:
		if err := checkKeyFile(s.keyPath); err != nil {
			return nil, fmt.Errorf("remotes: the key file: %w", err)
		}
		k, err := os.ReadFile(s.keyPath)
		if err != nil || len(k) != sealKeySize {
			return nil, fmt.Errorf("remotes: the key file is not a %d-byte key (%v)", sealKeySize, err)
		}
		return k, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	if anySealed(m) {
		xlog.Error("The remotes key file is missing but remotes.json holds keys sealed with it; no new key is made", "key_file", s.keyPath)
		return nil, errKeyMissing
	}
	k, err := generate()
	if err != nil {
		return nil, err
	}
	if err := atomicfile.Write(s.keyPath, k, 0o600); err != nil {
		return nil, fmt.Errorf("remotes: write the key file: %w", err)
	}
	return k, nil
}

// checkKeyFile refuses a key file that other users can open. On Windows a key
// file written before LocalAI set access control lists is made private
// instead, because it only had the list of its folder.
func checkKeyFile(path string) error {
	restricted, err := fsperm.CheckOrRestrict(path)
	if restricted {
		xlog.Warn("Other users could open the remotes key file; it is now private", "path", path)
	}
	return err
}
