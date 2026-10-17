// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
)

// Bounds of the data size read. The view is polled, so one read must cost a
// bounded amount of memory and time whatever the directory holds.
const (
	maxStorageFiles   = 20000
	storageBatch      = 256
	storageBudget     = 750 * time.Millisecond
	storageResultKeep = 30 * time.Second
)

// dirFile is the part of an open directory the scan uses. Reading in batches
// is the point: os.ReadDir and filepath.WalkDir read and sort a whole
// directory before the caller can stop, so a flat directory of 400000 entries
// costs about 70 MB even when the caller only wants 20000.
type dirFile interface {
	ReadDir(n int) ([]fs.DirEntry, error)
	Close() error
}

// storageScanner adds up the size of an addon data directory under a hard
// bound of entries and time, and keeps a result for a short time so a polling
// page does not walk the same tree again. The zero value uses the defaults.
type storageScanner struct {
	// Test seams.
	open   func(string) (dirFile, error)
	now    func() time.Time
	limit  int
	batch  int
	budget time.Duration
	keep   time.Duration

	scan  sync.Mutex // one walk at a time bounds the cost of concurrent polls
	mu    sync.Mutex
	cache map[string]storageResult
}

type storageResult struct {
	out apps.Storage
	at  time.Time
}

func (s *storageScanner) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *storageScanner) cached(dir string) (apps.Storage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.keep
	if keep == 0 {
		keep = storageResultKeep
	}
	r, ok := s.cache[dir]
	if !ok || s.clock().Sub(r.at) >= keep {
		return apps.Storage{}, false
	}
	return r.out, true
}

func (s *storageScanner) remember(dir string, out apps.Storage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]storageResult{}
	}
	keep := s.keep
	if keep == 0 {
		keep = storageResultKeep
	}
	now := s.clock()
	for k, r := range s.cache {
		if now.Sub(r.at) >= keep {
			delete(s.cache, k)
		}
	}
	s.cache[dir] = storageResult{out: out, at: now}
}

// size returns the bytes of the regular files below dir. Symbolic links are
// never followed or counted. Truncated is true when the entry or time bound
// stopped the walk. A missing directory is empty. A result cut short by the
// caller's context is not kept.
func (s *storageScanner) size(ctx context.Context, dir string) apps.Storage {
	if dir == "" {
		return apps.Storage{}
	}
	if out, ok := s.cached(dir); ok {
		return out
	}
	s.scan.Lock()
	defer s.scan.Unlock()
	if out, ok := s.cached(dir); ok {
		return out
	}
	out, complete := s.walk(ctx, dir)
	if complete {
		s.remember(dir, out)
	}
	return out
}

func (s *storageScanner) walk(ctx context.Context, root string) (apps.Storage, bool) {
	limit, batch, budget := s.limit, s.batch, s.budget
	if limit <= 0 {
		limit = maxStorageFiles
	}
	if batch <= 0 {
		batch = storageBatch
	}
	if budget <= 0 {
		budget = storageBudget
	}
	open := s.open
	if open == nil {
		open = func(p string) (dirFile, error) { return os.Open(p) } // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	}
	deadline := s.clock().Add(budget)
	var out apps.Storage
	seen := 0
	stop := func() bool {
		if ctx.Err() != nil {
			return true
		}
		if !s.clock().Before(deadline) {
			out.Truncated = true
			return true
		}
		return false
	}
	pending := []string{root}
	for len(pending) > 0 {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		f, e := open(dir)
		if e != nil {
			continue
		}
		for {
			if stop() {
				_ = f.Close()
				return out, ctx.Err() == nil
			}
			entries, e := f.ReadDir(batch)
			for _, d := range entries {
				seen++
				if seen > limit {
					out.Truncated = true
					_ = f.Close()
					return out, true
				}
				switch {
				case d.Type().IsRegular():
					if info, e := d.Info(); e == nil {
						out.Bytes += info.Size()
					}
				case d.IsDir():
					pending = append(pending, filepath.Join(dir, d.Name()))
				}
			}
			if e != nil {
				// End of the directory, or an unreadable one: it
				// contributes what was read.
				break
			}
		}
		_ = f.Close()
	}
	return out, ctx.Err() == nil
}
