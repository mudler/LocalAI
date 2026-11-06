// SPDX-License-Identifier: MIT
package musicapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/endpoints/localai/musicrequest"
	"github.com/mudler/LocalAI/core/services/music"
	"github.com/mudler/LocalAI/core/services/music/outputstore"
)

// Uploads is a dedicated private pre-resolution spool, never a static root.
// Application wiring must call Reclaim at startup and periodically. A single
// adapter owns a root; concurrent independent adapters must use different roots.
// Clock and directory are trusted explicit configuration, not HTTP fields.
type Uploads struct {
	mu      sync.Mutex
	root    *os.Root
	clock   func() time.Time
	records map[*musicrequest.OwnedUpload]*uploadRecord
	waiters map[*musicrequest.OwnedUpload][]chan error
	scanMu  sync.Mutex
	scan    *os.File
}
type uploadRecord struct {
	ID      string    `json:"id"`
	Request string    `json:"request_id"`
	Expires time.Time `json:"expires_at"`
	writer  *joinedWriter
}
type joinedWriter struct {
	mu     sync.Mutex
	f      *os.File
	closed bool
	count  int64
}

func (w *joinedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if int64(len(p))+w.count > musicrequest.AudioLimit {
		return 0, errors.New("upload limit")
	}
	n, e := w.f.Write(p)
	w.count += int64(n)
	return n, e
}
func (w *joinedWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return errors.Join(w.f.Sync(), w.f.Close())
}
func NewUploads(dir string, clock func() time.Time) (*Uploads, error) {
	if !filepath.IsAbs(dir) || clock == nil {
		return nil, errors.New("private upload configuration required")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	st, e := os.Lstat(dir)
	if e != nil {
		return nil, e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private upload root required")
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	return &Uploads{root: r, clock: clock, records: map[*musicrequest.OwnedUpload]*uploadRecord{}}, nil
}
func (u *Uploads) Close() error {
	u.scanMu.Lock()
	defer u.scanMu.Unlock()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.records) != 0 {
		return errors.New("uploads still owned")
	}
	if u.scan != nil {
		_ = u.scan.Close()
		u.scan = nil
	}
	return u.root.Close()
}
func ack(err error) music.CleanupTask {
	ch := make(chan error, 1)
	if err == nil {
		ch <- nil
	} else {
		ch <- err
	}
	return music.CleanupTask{Released: ch}
}
func (u *Uploads) Create(ctx context.Context) (*musicrequest.OwnedUpload, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	meta, ok := ctx.Value(entryKey{}).(entry)
	if !ok || !validID(meta.ID) || !meta.Deadline.After(u.clock()) || ctx.Err() != nil {
		return nil, errors.New("upload request context required")
	}
	if len(u.records) >= 64 {
		return nil, errors.New("upload capacity")
	}
	id, e := randomID()
	if e != nil {
		return nil, e
	}
	rec := &uploadRecord{ID: id, Request: meta.ID, Expires: meta.Deadline.Add(time.Hour)}
	// Persist and fsync the reclamation record BEFORE allocating recording bytes.
	data, e := json.Marshal(rec)
	if e != nil {
		return nil, e
	}
	f, e := u.root.OpenFile(id+".pending", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	_, we := f.Write(data)
	e = errors.Join(we, f.Sync(), f.Close())
	if e == nil {
		e = u.root.Rename(id+".pending", id+".json")
	}
	if e == nil {
		e = u.syncRoot()
	}
	if e != nil {
		_ = u.root.Remove(id + ".pending")
		_ = u.root.Remove(id + ".json")
		return nil, e
	}
	f, e = u.root.OpenFile(id+".audio", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		_ = u.root.Remove(id + ".json")
		return nil, e
	}
	rec.writer = &joinedWriter{f: f}
	owned := &musicrequest.OwnedUpload{Ref: id, Writer: rec.writer}
	owned.Cleanup = func() music.CleanupTask { return u.cleanup(owned) }
	u.records[owned] = rec
	return owned, nil
}
func (u *Uploads) syncRoot() error {
	f, e := u.root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func remove(root *os.Root, name string) error {
	e := root.Remove(name)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (u *Uploads) cleanup(owned *musicrequest.OwnedUpload) music.CleanupTask {
	// Failed deletions remain owned. Reclaim retries them and sends release only
	// after actual deletion; no closed-channel or failed-attempt fake ACK.
	ch := make(chan error, 1)
	u.mu.Lock()
	rec, ok := u.records[owned]
	if !ok {
		u.mu.Unlock()
		ch <- nil
		return music.CleanupTask{Released: ch}
	}
	e := rec.writer.Close()
	if e == nil {
		e = u.removeRecord(rec)
	}
	if e == nil {
		u.releaseWaiters(owned)
		u.mu.Unlock()
		ch <- nil
		return music.CleanupTask{Released: ch}
	}
	if u.waiters == nil {
		u.waiters = map[*musicrequest.OwnedUpload][]chan error{}
	}
	u.waiters[owned] = append(u.waiters[owned], ch)
	u.mu.Unlock()
	return music.CleanupTask{AttemptError: e, Released: ch}
}
func (u *Uploads) removeRecord(r *uploadRecord) error {
	if e := remove(u.root, r.ID+".audio"); e != nil {
		return e
	}
	if e := remove(u.root, r.ID+".pending"); e != nil {
		return e
	}
	if e := remove(u.root, r.ID+".json"); e != nil {
		return e
	}
	return u.syncRoot()
}

// releaseWaiters is called with mu held only after definitive physical cleanup.
// Every earlier caller retains its diagnostic, but receives the SAME release ACK.
func (u *Uploads) releaseWaiters(owned *musicrequest.OwnedUpload) {
	for _, ch := range u.waiters[owned] {
		ch <- nil
	}
	delete(u.waiters, owned)
	delete(u.records, owned)
}

// Reclaim advances one persistent directory iterator by at most 64 ENTRIES,
// including ineligible names. Directory scanning never holds the ownership mutex.
// scanMu serializes iterator/Close only; Create and cleanup use mu independently.
func (u *Uploads) Reclaim(ctx context.Context) error {
	u.scanMu.Lock()
	defer u.scanMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	u.mu.Lock()
	for owned := range u.waiters {
		rec := u.records[owned]
		if rec == nil {
			continue
		}
		if e := u.removeRecord(rec); e == nil {
			u.releaseWaiters(owned)
		}
	}
	u.mu.Unlock()
	if u.scan == nil {
		f, e := u.root.Open(".")
		if e != nil {
			return e
		}
		u.scan = f
	}
	entries, e := u.scan.ReadDir(64)
	if e == io.EOF {
		_ = u.scan.Close()
		u.scan = nil
	} else if e != nil {
		return e
	}
	var errs error
	for _, de := range entries {
		name := de.Name()
		id := strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".pending")
		if !(strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".pending")) || !validID(id) {
			continue
		}
		// Snapshot and validate this entry while holding mu: a live registration can
		// never become a stale unowned deletion target between validation and unlink.
		u.mu.Lock()
		live := false
		for _, r := range u.records {
			if r.ID == id {
				live = true
				break
			}
		}
		if !live {
			errs = errors.Join(errs, u.reclaimEntry(name, id))
		}
		u.mu.Unlock()
	}
	return errs
}
func (u *Uploads) reclaimEntry(name, id string) error {
	st, e := u.root.Lstat(name)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	f, e := u.root.Open(name)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(st, actual) || !actual.Mode().IsRegular() || actual.Size() > 1024 {
		return nil
	}
	var r uploadRecord
	if e = json.NewDecoder(io.LimitReader(f, 1025)).Decode(&r); e != nil || r.ID != id || !validID(r.Request) {
		return nil
	}
	if !r.Expires.After(u.clock()) {
		return u.removeRecord(&r)
	}
	return nil
}

// PrivateBridge imports actual spool bytes into the same concrete private Store
// used by Runner.Storage and Runner.Publisher. Both MUST be this bridge (not
// its underlying Store), so cancellation cannot retire input before settlement.
// It never issues ownership tokens. Store.Reconcile and Uploads.Reclaim still
// need application-owned periodic scheduling for crash/retry reclamation.
type PrivateBridge struct {
	*outputstore.Store
	uploads *Uploads
	mu      sync.Mutex
	inputs  map[string]chan struct{}
	sources map[string]music.OwnedInput
	bound   map[string]bool
}

func NewPrivateBridge(uploads *Uploads, store *outputstore.Store) (*PrivateBridge, error) {
	if uploads == nil || store == nil {
		return nil, errors.New("private bridge dependencies required")
	}
	return &PrivateBridge{Store: store, uploads: uploads, inputs: map[string]chan struct{}{}, sources: map[string]music.OwnedInput{}, bound: map[string]bool{}}, nil
}
func (b *PrivateBridge) Bind(ctx context.Context, in music.ValidatedRequest, owned *musicrequest.OwnedUpload) (music.OwnedInput, error) {
	b.mu.Lock()
	if b.bound[in.ID] {
		b.mu.Unlock()
		return music.OwnedInput{}, errors.New("request already bound")
	}
	b.bound[in.ID] = true
	b.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return music.OwnedInput{}, e
	}
	var reader *os.File
	if in.Operation != music.Generation {
		b.uploads.mu.Lock()
		rec, ok := b.uploads.records[owned]
		if !ok || rec.Request != in.ID {
			b.uploads.mu.Unlock()
			return music.OwnedInput{}, errors.New("upload ownership denied")
		}
		if e := rec.writer.Close(); e != nil {
			b.uploads.mu.Unlock()
			return music.OwnedInput{}, e
		}
		var e error
		reader, e = b.uploads.root.Open(rec.ID + ".audio")
		b.uploads.mu.Unlock()
		if e != nil {
			return music.OwnedInput{}, e
		}
		defer reader.Close()
	} else if owned != nil {
		return music.OwnedInput{}, errors.New("unexpected upload")
	}
	if e := b.Store.RegisterRequest(ctx, outputstore.RequestMetadata{ID: in.ID, Principal: in.Principal, Deadline: in.Deadline, TranscriptionModel: in.Transcription.CanonicalModel, GenerationModel: in.Generation.CanonicalModel}); e != nil {
		return music.OwnedInput{}, e
	}
	if reader == nil {
		return music.OwnedInput{}, nil
	}
	source, e := b.Store.ImportInput(ctx, in.ID, io.LimitReader(reader, musicrequest.AudioLimit+1))
	if e != nil && source != (music.OwnedInput{}) {
		e = errors.Join(e, b.cleanPartial(ctx, in.ID, source))
	}
	if e == nil {
		b.mu.Lock()
		b.inputs[in.ID] = make(chan struct{})
		b.sources[in.ID] = source
		b.mu.Unlock()
	}
	return source, e
}

// Revoke waits for the settled runner's input cleanup before whole-request
// deletion. Otherwise Store.Revoke can retire the registration before Runner's
// CleanInput lookup, which correctly refuses to ACK an unknown capability.
func (b *PrivateBridge) Revoke(ctx context.Context, id string) music.CleanupTask {
	ctx = context.WithoutCancel(ctx)
	b.mu.Lock()
	done := b.inputs[id]
	b.mu.Unlock()
	ch := make(chan error, 1)
	go func() {
		if done != nil {
			<-done
		}
		attempt, cancel := cleanupAttempt(ctx)
		defer cancel()
		task := b.Store.Revoke(attempt, id)
		err := task.Wait()
		if err != nil {
			ch <- err
		}
		ch <- nil
	}()
	return music.CleanupTask{Released: ch}
}
func (b *PrivateBridge) CleanInput(ctx context.Context, id string, in music.OwnedInput) music.CleanupTask {
	ctx = context.WithoutCancel(ctx)
	attempt, cancel := cleanupAttempt(ctx)
	task := b.Store.CleanInput(attempt, id, in)
	ch := make(chan error, 1)
	go func() {
		defer cancel()
		err := task.Wait()
		b.mu.Lock()
		if done := b.inputs[id]; done != nil {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		b.mu.Unlock()
		if err != nil {
			ch <- err
		}
		ch <- nil
	}()
	return music.CleanupTask{Released: ch}
}
func (b *PrivateBridge) Finish(ctx context.Context, id string) music.CleanupTask {
	ctx = context.WithoutCancel(ctx)
	var inputErr error
	b.mu.Lock()
	done := b.inputs[id]
	source := b.sources[id]
	b.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		default:
			inputErr = b.CleanInput(ctx, id, source).Wait()
		}
	}
	task := b.Revoke(ctx, id)
	ch := make(chan error, 1)
	go func() {
		err := errors.Join(inputErr, task.Wait())
		b.mu.Lock()
		delete(b.inputs, id)
		delete(b.sources, id)
		delete(b.bound, id)
		b.mu.Unlock()
		if err != nil {
			ch <- err
		}
		ch <- nil
	}()
	return music.CleanupTask{Released: ch}
}
func (b *PrivateBridge) Lookup(ctx context.Context, id string) (outputstore.Manifest, error) {
	return b.Store.Lookup(ctx, id)
}

func cleanupAttempt(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}

func (b *PrivateBridge) cleanPartial(ctx context.Context, id string, source music.OwnedInput) error {
	attempt, cancel := cleanupAttempt(ctx)
	defer cancel()
	return b.Store.CleanInput(attempt, id, source).Wait()
}
func (b *PrivateBridge) CleanArtifact(ctx context.Context, id string, c music.Candidate, p music.PreparedOutput) music.CleanupTask {
	attempt, cancel := cleanupAttempt(ctx)
	task := b.Store.CleanArtifact(attempt, id, c, p)
	ch := make(chan error, 1)
	go func() {
		defer cancel()
		err := task.Wait()
		if err != nil {
			ch <- err
		}
		ch <- nil
	}()
	return music.CleanupTask{Released: ch}
}
