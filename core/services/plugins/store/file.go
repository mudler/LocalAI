package store

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

// FileStore keeps the records in one JSON file, for a server without a
// database. Such a server has one frontend, so the mutex is enough to order
// the writes. Each call reads the file and each write replaces it
// atomically, so the file is the only copy of the state.
type FileStore struct {
	path string
	mu   sync.Mutex
	mem  *fileState // the state of a store with no file (tests)
}

type fileState struct {
	Marketplaces map[string]Marketplace `json:"marketplaces"`
	Snapshots    map[string]Snapshot    `json:"snapshots"`
	Packages     map[string]Package     `json:"packages"`
	Installs     map[string]Install     `json:"installs"`
	// UserStates is keyed by principal, then install ID.
	UserStates map[string]map[string]UserState `json:"userStates"`
	Tokens     map[string]Token                `json:"tokens"`
	// Inspections is keyed by plugin ID.
	Inspections map[string]Inspection `json:"inspections"`
}

func newFileState() *fileState {
	return &fileState{
		Marketplaces: map[string]Marketplace{}, Snapshots: map[string]Snapshot{},
		Packages: map[string]Package{}, Installs: map[string]Install{},
		UserStates: map[string]map[string]UserState{}, Tokens: map[string]Token{},
		Inspections: map[string]Inspection{},
	}
}

// NewFileStore opens the store in the file at path. An empty path keeps the
// records in memory only.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path}
	if path == "" {
		s.mem = newFileState()
		return s, nil
	}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileStore) load() (*fileState, error) {
	if s.mem != nil {
		return s.mem, nil
	}
	st := newFileState()
	b, err := os.ReadFile(filepath.Clean(s.path))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plugins store: read %s: %w", filepath.Base(s.path), err)
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("plugins store: parse %s: %w", filepath.Base(s.path), err)
	}
	// A file written by an older version may lack a map.
	fresh := newFileState()
	if st.Marketplaces == nil {
		st.Marketplaces = fresh.Marketplaces
	}
	if st.Snapshots == nil {
		st.Snapshots = fresh.Snapshots
	}
	if st.Packages == nil {
		st.Packages = fresh.Packages
	}
	if st.Installs == nil {
		st.Installs = fresh.Installs
	}
	if st.UserStates == nil {
		st.UserStates = fresh.UserStates
	}
	if st.Inspections == nil {
		st.Inspections = fresh.Inspections
	}
	if st.Tokens == nil {
		st.Tokens = fresh.Tokens
	}
	return st, nil
}

// save writes through a temporary file in the same folder, synced before the
// rename, so a crash never leaves half a file.
func (s *FileStore) save(st *fileState) error {
	if s.mem != nil {
		s.mem = st
		return nil
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".plugins-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}

// read runs fn on the state without writing it.
func (s *FileStore) read(fn func(*fileState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	return fn(st)
}

// write runs fn on a copy of the state and saves the copy when fn succeeds,
// so a failed fn changes nothing.
func (s *FileStore) write(fn func(*fileState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	next, err := st.clone()
	if err != nil {
		return err
	}
	if err := fn(next); err != nil {
		return err
	}
	return s.save(next)
}

func (st *fileState) clone() (*fileState, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	out := newFileState()
	return out, json.Unmarshal(b, out)
}

func (s *FileStore) Marketplaces(_ context.Context) ([]Marketplace, error) {
	var out []Marketplace
	err := s.read(func(st *fileState) error {
		for _, m := range st.Marketplaces {
			out = append(out, m)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

func (s *FileStore) SaveMarketplace(_ context.Context, m Marketplace) error {
	if err := validMarketplace(m); err != nil {
		return err
	}
	return s.write(func(st *fileState) error {
		old, ok := st.Marketplaces[m.ID]
		if ok && keepTombstone(old, m) {
			return nil
		}
		if ok && m.CreatedAt.IsZero() {
			m.CreatedAt = old.CreatedAt
		}
		st.Marketplaces[m.ID] = m
		return nil
	})
}

func (s *FileStore) DeleteMarketplace(_ context.Context, id string) error {
	return s.write(func(st *fileState) error {
		m, ok := st.Marketplaces[id]
		if !ok || m.Removed {
			return ErrNotFound
		}
		delete(st.Snapshots, id)
		if m.Default {
			m.Removed, m.Enabled = true, false
			st.Marketplaces[id] = m
			return nil
		}
		delete(st.Marketplaces, id)
		return nil
	})
}

func (s *FileStore) Snapshot(_ context.Context, id string) (Snapshot, error) {
	var out Snapshot
	err := s.read(func(st *fileState) error {
		snap, ok := st.Snapshots[id]
		if !ok {
			return ErrNotFound
		}
		out = snap
		return nil
	})
	return out, err
}

func (s *FileStore) SaveSnapshot(_ context.Context, snap Snapshot) error {
	if err := validSnapshot(snap); err != nil {
		return err
	}
	return s.write(func(st *fileState) error {
		st.Snapshots[snap.MarketplaceID] = snap
		return nil
	})
}

func (s *FileStore) Package(_ context.Context, digest string) (Package, error) {
	var out Package
	err := s.read(func(st *fileState) error { return fileTx{st}.getPackage(digest, &out) })
	return out, err
}

func (s *FileStore) PutPackage(_ context.Context, p Package) error {
	return s.write(func(st *fileState) error { return fileTx{st}.PutPackage(p) })
}

func (s *FileStore) UnreferencedPackages(_ context.Context, olderThan time.Time) ([]string, error) {
	var out []string
	err := s.read(func(st *fileState) error {
		installs := allInstalls(st)
		for d, p := range st.Packages {
			if p.CreatedAt.Before(olderThan) && !referenced(installs, d) {
				out = append(out, d)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func (s *FileStore) DeletePackage(_ context.Context, digest string) (bool, error) {
	deleted := false
	err := s.write(func(st *fileState) error {
		if _, ok := st.Packages[digest]; !ok || referenced(allInstalls(st), digest) {
			return nil
		}
		delete(st.Packages, digest)
		deleted = true
		return nil
	})
	return deleted, err
}

func (s *FileStore) Installs(_ context.Context, f InstallFilter) ([]Install, error) {
	var out []Install
	err := s.read(func(st *fileState) error {
		var err error
		out, err = fileTx{st}.Installs(f)
		return err
	})
	return out, err
}

func (s *FileStore) InstallTx(_ context.Context, fn func(Tx) error) error {
	return s.write(func(st *fileState) error { return fn(fileTx{st}) })
}

func (s *FileStore) UserState(_ context.Context, principal, installID string) (UserState, error) {
	var out UserState
	err := s.read(func(st *fileState) error {
		us, ok := st.UserStates[principal][installID]
		if !ok {
			return ErrNotFound
		}
		out = us
		return nil
	})
	return out, err
}

func (s *FileStore) UserStates(_ context.Context, principal string) ([]UserState, error) {
	var out []UserState
	err := s.read(func(st *fileState) error {
		for _, us := range st.UserStates[principal] {
			out = append(out, us)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].InstallID < out[j].InstallID })
	return out, err
}

func (s *FileStore) SaveUserState(_ context.Context, us UserState) error {
	return s.write(func(st *fileState) error { return fileTx{st}.SaveUserState(us) })
}

func (s *FileStore) PutToken(_ context.Context, t Token) error {
	if err := validToken(t); err != nil {
		return err
	}
	return s.write(func(st *fileState) error {
		if _, ok := st.Tokens[t.ID]; ok {
			return ErrConflict
		}
		st.Tokens[t.ID] = t
		return nil
	})
}

func (s *FileStore) Token(_ context.Context, id string, now time.Time) (Token, error) {
	var out Token
	err := s.read(func(st *fileState) error {
		t, ok := st.Tokens[id]
		if !ok || !t.ExpiresAt.After(now) {
			return ErrNotFound
		}
		out = t
		return nil
	})
	return out, err
}

func (s *FileStore) TakeToken(_ context.Context, id string, now time.Time) (Token, error) {
	var out Token
	err := s.write(func(st *fileState) error {
		t, ok := st.Tokens[id]
		if !ok || !t.ExpiresAt.After(now) {
			return ErrNotFound
		}
		delete(st.Tokens, id)
		out = t
		return nil
	})
	return out, err
}

func (s *FileStore) DeleteExpiredTokens(_ context.Context, now time.Time) (int, error) {
	n := 0
	err := s.write(func(st *fileState) error {
		for id, t := range st.Tokens {
			if !t.ExpiresAt.After(now) {
				delete(st.Tokens, id)
				n++
			}
		}
		return nil
	})
	return n, err
}

func (s *FileStore) MarkNeedsReenable(_ context.Context, installID string) error {
	return s.write(func(st *fileState) error { return fileTx{st}.MarkNeedsReenable(installID) })
}

func allInstalls(st *fileState) []Install {
	out := make([]Install, 0, len(st.Installs))
	for _, in := range st.Installs {
		out = append(out, in)
	}
	return out
}

// fileTx works on the copy of the state that FileStore.write saves.
type fileTx struct{ st *fileState }

func (t fileTx) Installs(f InstallFilter) ([]Install, error) {
	var out []Install
	for _, in := range t.st.Installs {
		if f.match(in) {
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (t fileTx) getPackage(digest string, out *Package) error {
	p, ok := t.st.Packages[digest]
	if !ok {
		return ErrNotFound
	}
	*out = p
	return nil
}

func (t fileTx) Package(digest string) (Package, error) {
	var out Package
	return out, t.getPackage(digest, &out)
}

func (t fileTx) PutPackage(p Package) error {
	if p.Digest == "" {
		return fmt.Errorf("%w: a package needs a digest", ErrInvalid)
	}
	if _, ok := t.st.Packages[p.Digest]; !ok {
		t.st.Packages[p.Digest] = p
	}
	return nil
}

func (t fileTx) CreateInstall(in Install) error {
	if err := validInstall(in); err != nil {
		return err
	}
	if _, ok := t.st.Installs[in.ID]; ok {
		return ErrConflict
	}
	for _, o := range t.st.Installs {
		if o.PluginID != in.PluginID {
			continue
		}
		if o.Scope == in.Scope && o.Owner == in.Owner {
			return ErrConflict
		}
		if in.Scope == ScopeUser && o.Scope == ScopeInstance {
			return &InstanceInstalledError{InstallID: o.ID}
		}
	}
	t.st.Installs[in.ID] = in
	return nil
}

func (t fileTx) UpdateInstall(in Install) error {
	if err := validInstall(in); err != nil {
		return err
	}
	old, ok := t.st.Installs[in.ID]
	if !ok {
		return ErrNotFound
	}
	if old.PluginID != in.PluginID || old.Scope != in.Scope || old.Owner != in.Owner {
		return fmt.Errorf("%w: an update cannot change the plugin, scope or owner", ErrInvalid)
	}
	in.CreatedAt = old.CreatedAt
	t.st.Installs[in.ID] = in
	return nil
}

func (t fileTx) DeleteInstall(id string) error {
	if _, ok := t.st.Installs[id]; !ok {
		return ErrNotFound
	}
	delete(t.st.Installs, id)
	for _, m := range t.st.UserStates {
		delete(m, id)
	}
	return nil
}

func (t fileTx) SaveUserState(us UserState) error {
	if us.Principal == "" || us.InstallID == "" {
		return fmt.Errorf("%w: a user state needs a principal and an install", ErrInvalid)
	}
	if _, ok := t.st.Installs[us.InstallID]; !ok {
		return ErrNotFound
	}
	if t.st.UserStates[us.Principal] == nil {
		t.st.UserStates[us.Principal] = map[string]UserState{}
	}
	t.st.UserStates[us.Principal][us.InstallID] = us
	return nil
}

func (t fileTx) MarkNeedsReenable(installID string) error {
	for p, m := range t.st.UserStates {
		if us, ok := m[installID]; ok {
			us.NeedsReenable = true
			t.st.UserStates[p][installID] = us
		}
	}
	return nil
}

func (s *FileStore) SaveInspection(_ context.Context, in Inspection) error {
	if in.PluginID == "" {
		return fmt.Errorf("%w: an inspection needs a plugin ID", ErrInvalid)
	}
	return s.write(func(st *fileState) error {
		st.Inspections[in.PluginID] = in
		return nil
	})
}

func (s *FileStore) Inspections(_ context.Context) ([]Inspection, error) {
	var out []Inspection
	err := s.read(func(st *fileState) error {
		out = make([]Inspection, 0, len(st.Inspections))
		for _, in := range st.Inspections {
			out = append(out, in)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].PluginID < out[j].PluginID })
	return out, err
}
