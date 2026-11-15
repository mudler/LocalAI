// SPDX-License-Identifier: MIT

package tracepersist

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
)

// ErrClosed is returned by writes to a store whose data directory was
// released with CloseUnder.
var ErrClosed = errors.New("trace store closed")

type Store[T any] struct {
	dir      string
	maxItems int
	mu       sync.Mutex
	lastSeq  int64
	// closed is set under mu, so setting it waits for a running write; it is
	// atomic so Closed, which callers query on every traced call, never
	// waits behind a write's fsync.
	closed atomic.Bool
}

// Trace records are written by background consumers that outlive the request
// which produced them. The registry lets the owner of a data directory stop
// those writes before it releases the directory (see CloseUnder).
var (
	registryMu sync.Mutex
	registry   = map[closer]struct{}{}
)

type closer interface {
	directory() string
	close()
}

func New[T any](dir string, maxItems int) (*Store[T], error) {
	if maxItems <= 0 {
		maxItems = 100
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &Store[T]{dir: dir, maxItems: maxItems}
	registryMu.Lock()
	registry[s] = struct{}{}
	registryMu.Unlock()
	return s, nil
}

// CloseUnder closes every store whose directory lies inside root. It returns
// once no write of such a store is in progress, and later writes to them are
// dropped with ErrClosed, so the caller may remove root right away.
func CloseUnder(root string) {
	if root == "" {
		return
	}
	registryMu.Lock()
	var matched []closer
	for s := range registry {
		if within(root, s.directory()) {
			matched = append(matched, s)
			delete(registry, s)
		}
	}
	registryMu.Unlock()
	for _, s := range matched {
		s.close()
	}
}

func within(root, dir string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Store[T]) directory() string { return s.dir }

// close waits for a running Append or Clear, because both hold s.mu for the
// whole file operation.
func (s *Store[T]) close() {
	s.mu.Lock()
	s.closed.Store(true)
	s.mu.Unlock()
}

// Closed reports whether the store was released with CloseUnder. Callers that
// cache a store use it to open a fresh one when the directory is reused.
func (s *Store[T]) Closed() bool {
	return s.closed.Load()
}

func (s *Store[T]) Load() ([]T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := s.files()
	if err != nil {
		return nil, err
	}
	records := make([]T, 0, len(files))
	for _, name := range files {
		// names comes directly from os.ReadDir(s.dir), so it cannot contain a
		// path separator or escape the store directory.
		// #nosec G304
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return nil, err
		}
		var record T
		if err := json.Unmarshal(data, &record); err != nil {
			xlog.Warn("Skipping corrupt persisted trace", "file", name, "error", err)
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *Store[T]) Append(id string, record T) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return ErrClosed
	}

	seq := time.Now().UnixNano()
	if seq <= s.lastSeq {
		seq = s.lastSeq + 1
	}
	s.lastSeq = seq
	name := fmt.Sprintf("%020d-%s.json", seq, hex.EncodeToString([]byte(id)))

	tmp, err := os.CreateTemp(s.dir, ".trace-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, name)); err != nil {
		return err
	}

	files, err := s.files()
	if err != nil {
		return err
	}
	for len(files) > s.maxItems {
		if err := os.Remove(filepath.Join(s.dir, files[0])); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		files = files[1:]
	}
	return nil
}

func (s *Store[T]) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return ErrClosed
	}

	files, err := s.files()
	if err != nil {
		return err
	}
	for _, name := range files {
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store[T]) files() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}
