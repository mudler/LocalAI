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
	"time"

	"github.com/mudler/xlog"
)

type Store[T any] struct {
	dir      string
	maxItems int
	mu       sync.Mutex
	lastSeq  int64
}

func New[T any](dir string, maxItems int) (*Store[T], error) {
	if maxItems <= 0 {
		maxItems = 100
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Store[T]{dir: dir, maxItems: maxItems}, nil
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
