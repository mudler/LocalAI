package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	// syncWorkers is how many syncs of files or folders run at the same time.
	// A sync waits for the disk, not for the CPU, and the disk finishes several
	// at once in about the time it needs for one, so a batch of blobs takes a
	// small multiple of one sync instead of a sum.
	syncWorkers = 16
	// syncChunk is how many staged files wait, open, before they are synced
	// together. It bounds the open files of a batch. The files are written
	// first and synced after, never at the same time: on macOS a sync that runs
	// beside new writes waits for them and does not overlap with the others.
	syncChunk = 64
)

// syncPool collects the syncs of one batch of blobs and runs them in chunks,
// with a bounded number of goroutines at a time.
type syncPool struct {
	mu      sync.Mutex
	pending []func() error
}

func newSyncPool() *syncPool { return &syncPool{} }

// submit adds a sync. It runs the chunk when it is full, and returns the
// errors of that run.
func (p *syncPool) submit(fn func() error) error {
	p.mu.Lock()
	p.pending = append(p.pending, fn)
	full := len(p.pending) >= syncChunk
	p.mu.Unlock()
	if full {
		return p.wait()
	}
	return nil
}

// wait runs the syncs that are pending and returns their errors. Every sync
// ends before it returns.
func (p *syncPool) wait() error {
	p.mu.Lock()
	jobs := p.pending
	p.pending = nil
	p.mu.Unlock()
	var (
		wg   sync.WaitGroup
		emu  sync.Mutex
		errs []error
	)
	sem := make(chan struct{}, syncWorkers)
	for _, fn := range jobs {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := fn(); err != nil {
				emu.Lock()
				errs = append(errs, err)
				emu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// batchStager is the capability of a Store that can sync the blobs of one
// batch together: Staging uses it when the store has it.
type batchStager interface {
	stageBatched(ctx context.Context, r io.Reader, max int64, p *syncPool) (Staged, error)
	publishBatch(ctx context.Context, staged []Staged) error
}

// publishBatch makes the staged blobs visible with the same guarantee as
// Publish on each one: when it returns without an error, every blob is
// complete, named and durable, and so is every folder that was made on the
// way. It syncs each folder once and the folders at the same time, instead of
// once for each blob. A staged value of another kind is published on its own.
func (s *fsStore) publishBatch(ctx context.Context, staged []Staged) error {
	dirs := map[string]struct{}{}
	for _, sg := range staged {
		t, ok := sg.(*fsStaged)
		if !ok || t.s != s {
			if err := sg.Publish(ctx); err != nil {
				return err
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		final := shaPath(s.root, t.sha)
		if err := s.mkdirCollect(filepath.Dir(final), dirs); err != nil {
			return err
		}
		if err := s.safe(final); err != nil {
			return err
		}
		if err := s.stage("rename"); err != nil {
			return err
		}
		// See Publish: a rename replaces identical bytes, and a name is never
		// removed after a sync failure.
		if err := os.Rename(t.path, final); err != nil {
			return err
		}
		dirs[filepath.Dir(final)] = struct{}{}
	}
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	p := newSyncPool()
	var errs []error
	for _, d := range names {
		if err := p.submit(func() error { return s.syncDirWith(d, s.barrierSync) }); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(append(errs, p.wait())...); err != nil {
		return err
	}
	if len(names) == 0 {
		return nil // nothing was written
	}
	return s.commitSync()
}

// mkdirCollect is mkdir that does not sync the parent of a folder it makes.
// It adds that parent to dirs, so the caller syncs it once.
func (s *fsStore) mkdirCollect(path string, dirs map[string]struct{}) error {
	if err := s.safe(path); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.mkdirCollect(filepath.Dir(path), dirs); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	dirs[filepath.Dir(path)] = struct{}{}
	return nil
}

func (s *fsStore) stageBatched(ctx context.Context, r io.Reader, max int64, p *syncPool) (Staged, error) {
	return s.stageFile(ctx, r, max, p)
}

var _ batchStager = (*fsStore)(nil)
