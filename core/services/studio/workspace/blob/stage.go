package blob

import (
	"context"
	"io"
)

// Staged is a blob whose bytes are read and hashed but not yet visible under
// its name.
type Staged interface {
	SHA() string
	Size() int64
	// Publish makes the blob visible. It is quick: the bytes are already
	// written. Calling it again after success is not supported.
	Publish(ctx context.Context) error
	// Discard drops the staged bytes. It is safe after Publish and safe to
	// call more than once.
	Discard()
}

// Stager is an optional capability of a Store that can split Put in two: the
// slow read of the upload (Stage) and the quick publication (Staged.Publish).
// The workspace stages outside its revision lock and publishes inside it.
type Stager interface {
	Stage(ctx context.Context, r io.Reader, max int64) (Staged, error)
}

// Staging is a Store whose Put only stages the bytes. Publish makes every
// staged blob visible and Close drops the ones that were not published. A
// store that is not a Stager is used as it is: its Put publishes at once and
// Publish has nothing to do.
//
// A store that can batch (the filesystem store) syncs the staged files in
// chunks, a few at a time, and syncs each folder once at Publish. A
// workspace commit of many files then takes a few disk round trips, not one
// or more for each file, with the same guarantee: when Publish returns, every
// blob is durable.
type Staging struct {
	Store
	stager Stager
	batch  batchStager
	pool   *syncPool
	staged []Staged
}

// NewStaging wraps st.
func NewStaging(st Store) *Staging {
	s := &Staging{Store: st}
	s.stager, _ = st.(Stager)
	if b, ok := st.(batchStager); ok {
		s.batch, s.pool = b, newSyncPool()
	}
	return s
}

// Put stages the bytes of r. The returned hash is not readable until Publish.
func (s *Staging) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	if s.stager == nil {
		return s.Store.Put(ctx, r, max)
	}
	var st Staged
	var err error
	if s.batch != nil {
		st, err = s.batch.stageBatched(ctx, r, max, s.pool)
	} else {
		st, err = s.stager.Stage(ctx, r, max)
	}
	if err != nil {
		return "", 0, err
	}
	s.staged = append(s.staged, st)
	return st.SHA(), st.Size(), nil
}

// Flush waits until the bytes of every staged blob are durable. Call it
// before the revision lock is taken, so the lock does not wait for the disk.
// Publish calls it as well, so a caller that does not is still correct.
func (s *Staging) Flush() error {
	if s.pool == nil {
		return nil
	}
	return s.pool.wait()
}

// Publish makes the staged blobs visible.
func (s *Staging) Publish(ctx context.Context) error {
	if err := s.Flush(); err != nil {
		return err
	}
	if s.batch != nil {
		return s.batch.publishBatch(ctx, s.staged)
	}
	for _, st := range s.staged {
		if err := st.Publish(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Close discards what Publish did not publish. Call it with defer.
func (s *Staging) Close() {
	if s.pool != nil {
		_ = s.pool.wait() // the syncs end before their files go
	}
	for _, st := range s.staged {
		st.Discard()
	}
	s.staged = nil
}

var _ Stager = (*fsStore)(nil)

// Volume is an optional capability of a Store whose bytes live on a volume
// that every node must share. The workspace keeps the identity in its
// database and compares it with the one on the volume at startup.
type Volume interface {
	// ReadVolumeID returns the identity stored on the volume, or "" if none.
	ReadVolumeID(ctx context.Context) (string, error)
	// WriteVolumeID stores id. It fails with os.ErrExist if an identity is there.
	WriteVolumeID(ctx context.Context, id string) error
}

var _ Volume = (*fsStore)(nil)
