// SPDX-License-Identifier: MIT
package config

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// headerCacheMaxFileBytes refuses to read a cache file larger than this. The
// entry bound already caps the size a LocalAI build writes, so anything bigger
// was written by something else and is not worth decoding.
const headerCacheMaxFileBytes = 64 << 20

// headerCacheSaveDebounce coalesces the inserts of a burst (a fresh start
// touching many files) into one write.
var headerCacheSaveDebounce = 2 * time.Second

// headerCacheRename is os.Rename behind a variable so a test can simulate a
// failed commit and check that the previous file survives.
var headerCacheRename = os.Rename

// headerCacheSpec describes one file format's use of the header cache. S is the
// snapshot kept per file path. The cache never looks inside S: everything that
// is format-specific comes from the spec.
type headerCacheSpec[S any] struct {
	// name prefixes log messages, for example "GGUF header cache".
	name string
	// filePrefix starts every cache file name. It ends with "-" so the instance
	// ID can follow it; standalone mode drops that dash.
	filePrefix string
	// schema is the on-disk format version. Bump it when encode changes shape.
	schema int
	// fingerprint names the inputs that decide the snapshot values. A file
	// written with another fingerprint may hold different values, so it is
	// ignored.
	fingerprint string
	// parse builds a snapshot from a file path. Its errors are never cached.
	parse func(path string) (S, error)
	// encode returns the JSON-serialisable form of a snapshot.
	encode func(S) any
	// decode rebuilds a snapshot from what encode wrote. An error means the
	// file is corrupt, so the whole file is ignored rather than partly loaded.
	decode func(json.RawMessage) (S, error)
}

// headerIdentity is what a cached snapshot is validated against. A changed size
// or modification time means the file was rewritten, so the entry is not used.
type headerIdentity struct {
	size  int64
	mtime int64 // UnixNano
}

type headerEntry[S any] struct {
	path     string
	identity headerIdentity
	snap     S
}

// headerCache is a bounded LRU keyed by absolute path, optionally persisted to
// one file per instance. It is safe for concurrent use. Lookups hold the lock
// only for the map and list operations, never while a file is parsed.
type headerCache[S any] struct {
	spec  headerCacheSpec[S]
	mu    sync.Mutex
	max   int
	order *list.List // front is most recently used; values are *headerEntry[S]
	index map[string]*list.Element

	// Disk persistence, set by attachDisk. Empty persistPath means memory only.
	persistPath string
	dirty       bool
	saveCh      chan struct{}
	stopSaver   context.CancelFunc
	saverDone   chan struct{}
	// persistMu serializes file writes so the saver and an explicit flush never
	// race on the same temp name or rename order.
	persistMu sync.Mutex
}

func newHeaderCache[S any](spec headerCacheSpec[S], max int) *headerCache[S] {
	return &headerCache[S]{spec: spec, max: max, order: list.New(), index: make(map[string]*list.Element)}
}

func (c *headerCache[S]) get(path string, id headerIdentity) (S, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.index[path]
	if !ok {
		var zero S
		return zero, false
	}
	e := el.Value.(*headerEntry[S])
	if e.identity != id {
		// The file changed since it was cached. Drop the stale entry now so the
		// fresh parse replaces it rather than sitting beside it.
		c.order.Remove(el)
		delete(c.index, path)
		c.markDirtyLocked()
		var zero S
		return zero, false
	}
	c.order.MoveToFront(el)
	return e.snap, true
}

// put stores a snapshot and schedules a background save when the cache is
// persisted. It never touches the disk itself, so it is safe on the request path.
func (c *headerCache[S]) put(path string, id headerIdentity, snap S) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.insertLocked(path, id, snap)
	c.markDirtyLocked()
}

// insertLocked adds or replaces an entry and enforces the bound. It does not
// mark the cache dirty, so loading from disk does not schedule a rewrite.
func (c *headerCache[S]) insertLocked(path string, id headerIdentity, snap S) {
	if el, ok := c.index[path]; ok {
		e := el.Value.(*headerEntry[S])
		e.identity, e.snap = id, snap
		c.order.MoveToFront(el)
		return
	}
	c.index[path] = c.order.PushFront(&headerEntry[S]{path: path, identity: id, snap: snap})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.index, oldest.Value.(*headerEntry[S]).path)
	}
}

func (c *headerCache[S]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// load returns the snapshot for path using the spec's parser.
func (c *headerCache[S]) load(path string) (S, error) {
	return c.loadVia(path, c.spec.parse)
}

// loadVia returns the snapshot for path, calling parse only when no cached
// snapshot matches the file's current identity. A file that cannot be stat'ed
// or parsed is never cached, so the caller sees the same error path as before
// the cache existed. Callers pass parse to swap in a test seam per call.
func (c *headerCache[S]) loadVia(path string, parse func(string) (S, error)) (S, error) {
	abs, absErr := filepath.Abs(path)
	info, statErr := os.Stat(path)
	if absErr != nil || statErr != nil {
		return parse(path)
	}

	// The identity is read before the parse. If the file is rewritten during the
	// parse, the entry is keyed by the older identity and the next call misses.
	id := headerIdentity{size: info.Size(), mtime: info.ModTime().UnixNano()}
	if snap, ok := c.get(abs, id); ok {
		return snap, nil
	}

	snap, err := parse(path)
	if err != nil {
		var zero S
		return zero, err
	}
	c.put(abs, id, snap)
	return snap, nil
}

// headerCachePath returns the cache file for an instance. An empty ID
// (standalone mode) uses the prefix without its trailing dash.
func headerCachePath(filePrefix, dataPath, instanceID string) string {
	name := strings.TrimSuffix(filePrefix, "-") + ".json"
	if instanceID != "" {
		key := instanceID
		if !headerInstanceIDPattern.MatchString(instanceID) {
			sum := sha256.Sum256([]byte(instanceID))
			key = "h-" + hex.EncodeToString(sum[:8])
		}
		name = filePrefix + key + ".json"
	}
	return filepath.Join(dataPath, name)
}

// headerInstanceIDPattern is the set of instance IDs used verbatim in a file
// name. Any other ID is hashed, so an ID cannot carry a path separator.
var headerInstanceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// headerFile is the whole cache file: one per frontend instance.
type headerFile struct {
	Schema int `json:"schema"`
	// Fingerprint is stored under the key "estimator", the name the GGUF cache
	// wrote before the cache was generic. The key is kept so existing files stay
	// valid.
	Fingerprint string            `json:"estimator"`
	Entries     []headerFileEntry `json:"entries"`
}

type headerFileEntry struct {
	Path    string          `json:"path"`
	Size    int64           `json:"size"`
	MtimeNS int64           `json:"mtime_ns"`
	Header  json.RawMessage `json:"header"`
}

// attachDisk makes the cache persistent under dataPath for instanceID. It loads
// the existing file once, then starts the background saver. An empty dataPath
// keeps the cache in memory only. Calling it again replaces the previous attach.
func (c *headerCache[S]) attachDisk(parent context.Context, dataPath, instanceID string) {
	c.stopPersist()
	if dataPath == "" {
		return
	}
	path := headerCachePath(c.spec.filePrefix, dataPath, instanceID)
	c.loadFile(path)

	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	saveCh := make(chan struct{}, 1)
	c.mu.Lock()
	c.persistPath = path
	c.saveCh = saveCh
	c.stopSaver = cancel
	c.saverDone = done
	c.mu.Unlock()

	go func() {
		defer close(done)
		c.runSaver(ctx, saveCh)
	}()
}

// stopPersist stops the saver after a final write of any pending change and
// disables persistence. It is safe to call when nothing is attached.
func (c *headerCache[S]) stopPersist() {
	c.mu.Lock()
	cancel, done := c.stopSaver, c.saverDone
	c.stopSaver, c.saverDone = nil, nil
	c.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done

	c.mu.Lock()
	c.persistPath = ""
	c.saveCh = nil
	c.dirty = false
	c.mu.Unlock()
}

// markDirtyLocked records a change and wakes the saver. The send never blocks:
// one pending signal is enough because the saver writes the whole cache.
func (c *headerCache[S]) markDirtyLocked() {
	if c.persistPath == "" {
		return
	}
	c.dirty = true
	select {
	case c.saveCh <- struct{}{}:
	default:
	}
}

// runSaver writes the cache after changes, debounced, and once more on shutdown.
// It is the only background writer; request paths never touch the disk.
func (c *headerCache[S]) runSaver(ctx context.Context, saveCh <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			c.flushIfDirty()
			return
		case <-saveCh:
			timer := time.NewTimer(headerCacheSaveDebounce)
			select {
			case <-ctx.Done():
				timer.Stop()
				c.flushIfDirty()
				return
			case <-timer.C:
			}
			c.flushIfDirty()
		}
	}
}

func (c *headerCache[S]) flushIfDirty() {
	c.mu.Lock()
	dirty := c.dirty
	c.mu.Unlock()
	if !dirty {
		return
	}
	if err := c.flush(); err != nil {
		xlog.Warn(c.spec.name+": could not save, it will retry on the next change", "error", err)
	}
}

// flush writes the current cache to its file now. It returns nil and writes
// nothing when the cache is not persistent.
func (c *headerCache[S]) flush() error {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	type pending struct {
		path string
		id   headerIdentity
		snap S
	}
	c.mu.Lock()
	path := c.persistPath
	if path == "" {
		c.mu.Unlock()
		return nil
	}
	// Copy under the lock and encode after it: encoding is the slow part and
	// must not hold up request lookups.
	snaps := make([]pending, 0, c.order.Len())
	for el := c.order.Front(); el != nil; el = el.Next() {
		e := el.Value.(*headerEntry[S])
		snaps = append(snaps, pending{path: e.path, id: e.identity, snap: e.snap})
	}
	c.dirty = false
	c.mu.Unlock()

	file := headerFile{
		Schema:      c.spec.schema,
		Fingerprint: c.spec.fingerprint,
		Entries:     make([]headerFileEntry, 0, len(snaps)),
	}
	data, err := func() ([]byte, error) {
		for _, p := range snaps {
			raw, err := json.Marshal(c.spec.encode(p.snap))
			if err != nil {
				return nil, err
			}
			file.Entries = append(file.Entries, headerFileEntry{
				Path:    p.path,
				Size:    p.id.size,
				MtimeNS: p.id.mtime,
				Header:  raw,
			})
		}
		return json.Marshal(file)
	}()
	if err == nil {
		err = writeFileAtomic(path, data)
	}
	if err != nil {
		c.mu.Lock()
		c.dirty = true
		c.mu.Unlock()
		return err
	}
	xlog.Debug(c.spec.name+" saved", "path", path, "entries", len(file.Entries))
	return nil
}

// loadFile fills the cache from path. A missing, unreadable, oversized, corrupt
// or mismatched file is ignored and logged once. Entries are not trusted here:
// the identity check at lookup decides whether each one is used.
func (c *headerCache[S]) loadFile(path string) {
	name := c.spec.name
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		xlog.Debug(name+": no file yet", "path", path)
		return
	}
	if err != nil {
		xlog.Warn(name+": cannot stat file, starting empty", "path", path, "error", err)
		return
	}
	if info.Size() > headerCacheMaxFileBytes {
		xlog.Warn(name+": file too large, ignoring it", "path", path, "bytes", info.Size())
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		xlog.Warn(name+": cannot read file, starting empty", "path", path, "error", err)
		return
	}
	var file headerFile
	if err := json.Unmarshal(raw, &file); err != nil {
		xlog.Warn(name+": file is corrupt, ignoring it", "path", path, "error", err)
		return
	}
	if file.Schema != c.spec.schema {
		xlog.Warn(name+": unsupported schema, ignoring it", "path", path, "schema", file.Schema)
		return
	}
	if file.Fingerprint != c.spec.fingerprint {
		xlog.Debug(name+": format fingerprint changed, ignoring file", "path", path)
		return
	}

	type loaded struct {
		path string
		id   headerIdentity
		snap S
	}
	decoded := make([]loaded, 0, len(file.Entries))
	for _, e := range file.Entries {
		snap, err := c.spec.decode(e.Header)
		if err != nil {
			xlog.Warn(name+": file is corrupt, ignoring it", "path", path, "error", err)
			return
		}
		decoded = append(decoded, loaded{path: e.Path, id: headerIdentity{size: e.Size, mtime: e.MtimeNS}, snap: snap})
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// The file lists the most recently used entry first. Keep the first max
	// entries, then insert from the back so the first one ends at the front.
	if len(decoded) > c.max {
		decoded = decoded[:c.max]
	}
	for i := len(decoded) - 1; i >= 0; i-- {
		d := decoded[i]
		if d.path == "" || !filepath.IsAbs(d.path) {
			continue
		}
		if _, exists := c.index[d.path]; exists {
			continue
		}
		c.insertLocked(d.path, d.id, d.snap)
	}
}

// writeFileAtomic writes data to a temp file in path's directory and renames it
// into place, so a reader sees either the old file or the new one. On any
// failure the temp file is removed and the previous file is left as it was.
func writeFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return headerCacheRename(tmp.Name(), path)
}
