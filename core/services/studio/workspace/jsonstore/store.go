// Package jsonstore is a workspace.Store that keeps its index as JSON files in
// a folder, for a server that has no database. It is the counterpart of the
// gormstore package: both pass the same contract suite (wstest), so files,
// uploads, trash and undo behave the same on either.
//
// The folder holds:
//
//	index.json   a snapshot of the whole index, replaced atomically
//	index.log    one JSON record per committed change, appended and synced
//
// The bytes of files live in a blob.Store, named by their SHA-256, as with
// gormstore. A commit appends one record to the log and syncs it before the
// call returns, so a crash loses at most a change that was never reported as
// done. At start the store reads the snapshot, replays the log, drops a torn
// last record, and folds the log into a new snapshot when it has grown.
//
// The index is held in memory behind one lock, which makes every call atomic
// inside one process. One process owns the folder: a second LocalAI process
// (or a second frontend of a cluster) on the same folder would overwrite the
// first one's index. A cluster uses the gormstore on a shared database. A
// process refuses to open a folder that it has open already.
package jsonstore

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

const (
	snapshotVersion = 1
	snapshotName    = "index.json"
	logName         = "index.log"
	// defaultCompactAt is the size of the log in bytes after which a commit
	// folds it into a new snapshot.
	defaultCompactAt = 16 << 20
)

// Options configures a Store.
type Options struct {
	// Admin is the principal that manages grants. It has write access to
	// every node. Empty means no admin: Grant and Revoke always fail.
	Admin string
	// MaxFileSize limits each file in bytes. Zero means no limit.
	MaxFileSize int64
	// Now returns the time recorded in change sets. Nil means time.Now.
	Now func() time.Time
	// CompactAt is the size of the change log in bytes after which a commit
	// folds it into a new snapshot. Zero means 16 MiB.
	CompactAt int64
}

// Store is a workspace.Store on a folder of JSON files and a blob.Store.
type Store struct {
	mu        sync.RWMutex
	d         *data
	dir       string // empty: memory only
	log       *os.File
	logSize   int64
	blobs     blob.Store
	admin     string
	maxSize   int64
	now       func() time.Time
	compactAt int64
}

var _ workspace.Store = (*Store)(nil)

var (
	openMu   sync.Mutex
	openDirs = map[string]struct{}{}
)

// New opens the store in dir and creates the folder when it is missing. With
// an empty dir the index lives in memory only and is lost on exit.
func New(dir string, blobs blob.Store, o Options) (*Store, error) {
	if blobs == nil {
		return nil, errors.New("workspace: a blob store is required")
	}
	s := &Store{d: newData(), blobs: blobs, admin: o.Admin, maxSize: o.MaxFileSize, now: o.Now, compactAt: o.CompactAt}
	if s.now == nil {
		s.now = time.Now
	}
	if s.compactAt <= 0 {
		s.compactAt = defaultCompactAt
	}
	if dir == "" {
		return s, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	openMu.Lock()
	defer openMu.Unlock()
	if _, taken := openDirs[abs]; taken {
		return nil, fmt.Errorf("workspace: the folder %q is open in this process already", abs)
	}
	s.dir = abs
	if err := s.load(); err != nil {
		return nil, err
	}
	openDirs[abs] = struct{}{}
	return s, nil
}

// Close releases the folder. The store must not be used afterwards.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return nil
	}
	openMu.Lock()
	delete(openDirs, s.dir)
	openMu.Unlock()
	s.dir = ""
	if s.log == nil {
		return nil
	}
	err := s.log.Close()
	s.log = nil
	return err
}

// limit returns the smaller non-zero of the store limit and a per-call limit.
func (s *Store) limit(perCall int64) int64 {
	switch {
	case s.maxSize == 0:
		return perCall
	case perCall == 0 || perCall > s.maxSize:
		return s.maxSize
	default:
		return perCall
	}
}

// kindFor returns the change set kind for a call by a.
func (s *Store) kindFor(a workspace.Actor) workspace.ChangeSetKind {
	if a.ChatID != "" {
		return workspace.ChangeAgent
	}
	return workspace.ChangeUser
}

func (s *Store) isAdmin(a workspace.Actor) bool {
	return s.admin != "" && a.Principal == s.admin
}

// snapshot is the file index.json.
type snapshot struct {
	Version int          `json:"version"`
	Seq     int64        `json:"seq"`
	Rev     int64        `json:"rev"`
	NextID  int64        `json:"next_id"`
	Nodes   []nodeRow    `json:"nodes"`
	Journal []journalRow `json:"journal"`
	Sets    []changeRec  `json:"sets"`
	Grants  []grantRec   `json:"grants"`
}

// record is one line of index.log: what one commit or one grant change wrote.
type record struct {
	Seq     int64        `json:"seq"`
	Nodes   []nodeRow    `json:"nodes,omitempty"`
	Journal []journalRow `json:"journal,omitempty"`
	Set     *changeRec   `json:"set,omitempty"`
	Grants  []grantRec   `json:"grants,omitempty"`
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

// load reads the snapshot, replays the log and opens the log for appending.
func (s *Store) load() error {
	raw, err := os.ReadFile(s.path(snapshotName))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		var snap snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			return fmt.Errorf("workspace: the index %s is damaged: %w", s.path(snapshotName), err)
		}
		if snap.Version != snapshotVersion {
			return fmt.Errorf("workspace: the index %s has version %d, want %d", s.path(snapshotName), snap.Version, snapshotVersion)
		}
		s.d.restore(snap)
	}
	if err := s.replay(); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(logName), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	s.log = f
	if st, err := f.Stat(); err == nil {
		s.logSize = st.Size()
	}
	if s.logSize > 1<<20 {
		if err := s.compact(); err != nil {
			xlog.Warn("workspace: could not fold the log into the index", "error", err)
		}
	}
	return nil
}

// replay applies the records of the log that the snapshot does not have. It
// stops at the first record it cannot read (a write that a crash cut short),
// keeps the damaged file next to the log and cuts the log back to the good
// records.
func (s *Store) replay() error {
	f, err := os.Open(s.path(logName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var good int64
	for {
		line, rerr := r.ReadBytes('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return rerr
		}
		complete := len(line) > 0 && line[len(line)-1] == '\n'
		var rec record
		if len(bytes.TrimSpace(line)) > 0 {
			if !complete || json.Unmarshal(line, &rec) != nil {
				return s.cutLog(good)
			}
			s.d.apply(rec)
		}
		good += int64(len(line))
		if rerr != nil {
			return nil
		}
	}
}

// cutLog keeps a copy of the damaged log and truncates it to size bytes.
func (s *Store) cutLog(size int64) error {
	xlog.Warn("workspace: the change log ends in a record that cannot be read; dropping it", "file", s.path(logName), "keptBytes", size)
	bad := s.path(logName) + ".damaged-" + s.now().UTC().Format("20060102T150405")
	if err := copyFile(s.path(logName), bad); err != nil {
		return err
	}
	return os.Truncate(s.path(logName), size)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- a file of the store folder
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a file of the store folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// restore loads a snapshot into an empty data.
func (d *data) restore(snap snapshot) {
	d.rev, d.seq = snap.Rev, snap.Seq
	d.nextID = max(d.nextID, snap.NextID)
	for _, n := range snap.Nodes {
		d.putLoaded(n)
	}
	for _, r := range snap.Journal {
		d.journal = append(d.journal, r)
		d.indexJournal(len(d.journal) - 1)
	}
	for i := range snap.Sets {
		d.addSet(snap.Sets[i])
	}
	for _, g := range snap.Grants {
		d.grants[grantKey{g.Principal, g.Node}] = g.Mode
	}
}

// putLoaded stores a node read from disk, without the checks and the log of
// setNode.
func (d *data) putLoaded(n nodeRow) {
	n.fold = workspace.FoldName(n.Name)
	if old := d.nodes[n.ID]; old != nil {
		d.unindex(old)
	}
	d.nodes[n.ID] = &n
	d.index(&n)
	d.nextID = max(d.nextID, n.ID+1)
}

func (d *data) addSet(c changeRec) {
	if _, ok := d.sets[c.Rev]; ok {
		return
	}
	cp := c
	d.sets[c.Rev] = &cp
	d.revs = append(d.revs, c.Rev)
	d.rev = max(d.rev, c.Rev)
}

// apply replays one log record. A record the snapshot already has is skipped.
func (d *data) apply(rec record) {
	if rec.Seq <= d.seq {
		return
	}
	d.seq = rec.Seq
	for _, n := range rec.Nodes {
		d.putLoaded(n)
	}
	for _, r := range rec.Journal {
		if r.ID <= int64(len(d.journal)) {
			continue
		}
		d.journal = append(d.journal, r)
		d.indexJournal(len(d.journal) - 1)
	}
	if rec.Set != nil {
		d.addSet(*rec.Set)
	}
	for _, g := range rec.Grants {
		if g.Mode == workspace.ModeNone {
			delete(d.grants, grantKey{g.Principal, g.Node})
			continue
		}
		d.grants[grantKey{g.Principal, g.Node}] = g.Mode
	}
}

// persist appends rec to the log and syncs it. With no folder it does nothing.
// The caller holds the write lock.
func (s *Store) persist(rec record) error {
	if s.dir == "" {
		s.d.seq++
		return nil
	}
	rec.Seq = s.d.seq + 1
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := s.log.Write(line); err != nil {
		// A partial line may be on disk. Cut it so that the next record
		// starts on a line of its own.
		_ = s.log.Truncate(s.logSize)
		return fmt.Errorf("workspace: write the change log: %w", err)
	}
	if err := s.log.Sync(); err != nil {
		_ = s.log.Truncate(s.logSize)
		return fmt.Errorf("workspace: sync the change log: %w", err)
	}
	s.logSize += int64(len(line))
	s.d.seq = rec.Seq
	return nil
}

// maybeCompact folds the log into a snapshot when it has grown. The caller
// holds the write lock and has applied the change that persist wrote.
func (s *Store) maybeCompact() {
	if s.dir == "" || s.logSize <= s.compactAt {
		return
	}
	if err := s.compact(); err != nil {
		xlog.Warn("workspace: could not fold the log into the index", "error", err)
	}
}

// compact writes the whole index to a new snapshot, replaces the old one with
// a rename and empties the log. A crash between the two steps leaves records
// that the snapshot has; replay skips them by their sequence number.
func (s *Store) compact() error {
	snap := s.d.snapshot()
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "index-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path(snapshotName)); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	if err := s.log.Truncate(0); err != nil {
		return err
	}
	s.logSize = 0
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- the store folder
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

func (d *data) snapshot() snapshot {
	snap := snapshot{Version: snapshotVersion, Seq: d.seq, Rev: d.rev, NextID: d.nextID, Journal: d.journal}
	for _, n := range d.nodes {
		snap.Nodes = append(snap.Nodes, *n)
	}
	slices.SortFunc(snap.Nodes, func(a, b nodeRow) int { return cmp.Compare(a.ID, b.ID) })
	for _, rev := range d.revs {
		snap.Sets = append(snap.Sets, *d.sets[rev])
	}
	for k, m := range d.grants {
		snap.Grants = append(snap.Grants, grantRec{Principal: k.Principal, Node: k.Node, Mode: m})
	}
	slices.SortFunc(snap.Grants, func(a, b grantRec) int {
		return cmp.Or(cmp.Compare(a.Node, b.Node), cmp.Compare(a.Principal, b.Principal))
	})
	return snap
}

// readLocked runs fn under the read lock.
func (s *Store) readLocked(ctx context.Context, fn func(d *data) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(s.d)
}
