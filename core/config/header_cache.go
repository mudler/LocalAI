// SPDX-License-Identifier: MIT
package config

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// headerCacheFileName is the one cache file every frontend uses, in the data
// path. Frontends that share the data volume share this file: see the
// shared-by-file mode in docs/content/advanced/model-configuration.md.
const headerCacheFileName = "header-cache.json"

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
// the shared header-cache.json. It is safe for concurrent use. Lookups hold the
// lock only for the map and list operations, never while a file is parsed or
// read.
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
	// loadedMtime is the modification time of the cache file the last time it
	// was read, UnixNano. Zero means it has not been read. A miss compares it
	// with the file on disk to decide whether another frontend has written.
	loadedMtime int64
	// persistMu serializes file writes so the saver and an explicit flush never
	// race on the same temp name or rename order.
	persistMu sync.Mutex
	// reloadMu keeps concurrent misses from each reading the same file.
	reloadMu sync.Mutex
}

func newHeaderCache[S any](spec headerCacheSpec[S], max int) *headerCache[S] {
	return &headerCache[S]{spec: spec, max: max, order: list.New(), index: make(map[string]*list.Element)}
}

// get returns the cached snapshot for path when its identity matches id. It
// never reads the disk: a hit is served from memory alone.
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
	c.evictLocked()
}

// insertBackLocked adds an entry as the least recently used one. Entries
// merged from the file use it, so that what this frontend used itself keeps
// its place ahead of what only another frontend used.
func (c *headerCache[S]) insertBackLocked(path string, id headerIdentity, snap S) {
	c.index[path] = c.order.PushBack(&headerEntry[S]{path: path, identity: id, snap: snap})
	c.evictLocked()
}

// evictLocked drops the least recently used entries until the bound holds.
func (c *headerCache[S]) evictLocked() {
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
//
// On a miss the shared cache file is checked once: if another frontend has
// written it since this one last read it, its entries are merged in before the
// parser runs. A hit never touches the cache file.
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
	c.reloadIfChanged()
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

// reloadIfChanged merges the cache file into memory when its modification time
// differs from the one recorded at the last read. It does nothing when the
// cache is not persistent or the file is absent.
func (c *headerCache[S]) reloadIfChanged() {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()

	c.mu.Lock()
	path, loaded := c.persistPath, c.loadedMtime
	c.mu.Unlock()
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.ModTime().UnixNano() == loaded {
		return
	}
	c.readAndMerge(path)
}

// headerFile is the whole cache file, shared by every frontend on the data path.
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

// attachDisk makes the cache persistent under dataPath, in the shared file. It
// loads the existing file once, then starts the background saver. An empty
// dataPath keeps the cache in memory only. Calling it again replaces the
// previous attach.
func (c *headerCache[S]) attachDisk(parent context.Context, dataPath string) {
	c.stopPersist()
	if dataPath == "" {
		return
	}
	path := headerCachePath(dataPath)
	c.readAndMerge(path)

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

// headerCachePath returns the shared cache file in dataPath.
func headerCachePath(dataPath string) string {
	return filepath.Join(dataPath, headerCacheFileName)
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
	c.loadedMtime = 0
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

// flush merges the shared file into memory, drops entries whose model file has
// changed, and writes the result back. It returns nil and writes nothing when
// the cache is not persistent.
//
// Several frontends may write the file at once. Each write is a whole-file
// rename, so a reader never sees a partial file. Two writers can still race: a
// writer that read the file before the other renamed can replace the other's
// new entries with its own. The loss is at worst a dropped entry, which costs
// one re-parse on its next use. A wrong value cannot result, because every
// entry is validated against its model file's identity before it is used.
func (c *headerCache[S]) flush() error {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	path := c.persistPath
	c.mu.Unlock()
	if path == "" {
		return nil
	}

	// Fold in what other frontends saved since this one last read the file, so
	// this write does not drop their entries.
	c.readAndMerge(path)

	type pending struct {
		path string
		id   headerIdentity
		snap S
	}
	c.mu.Lock()
	// Copy under the lock and encode after it: encoding is the slow part and
	// must not hold up request lookups.
	snaps := make([]pending, 0, c.order.Len())
	for el := c.order.Front(); el != nil; el = el.Next() {
		e := el.Value.(*headerEntry[S])
		snaps = append(snaps, pending{path: e.path, id: e.identity, snap: e.snap})
	}
	c.dirty = false
	c.mu.Unlock()

	// An entry whose model file has been rewritten since it was cached can
	// never be used, so it is not written. A file that cannot be stat'ed is
	// kept: the identity check at lookup still decides whether it is used.
	stale := make(map[string]headerIdentity)
	for _, p := range snaps {
		info, err := os.Stat(p.path)
		if err != nil {
			continue
		}
		if now := (headerIdentity{size: info.Size(), mtime: info.ModTime().UnixNano()}); now != p.id {
			stale[p.path] = p.id
		}
	}
	if len(stale) > 0 {
		c.mu.Lock()
		for p, id := range stale {
			if el, ok := c.index[p]; ok && el.Value.(*headerEntry[S]).identity == id {
				c.order.Remove(el)
				delete(c.index, p)
			}
		}
		c.mu.Unlock()
	}

	file := headerFile{
		Schema:      c.spec.schema,
		Fingerprint: c.spec.fingerprint,
		Entries:     make([]headerFileEntry, 0, len(snaps)),
	}
	data, err := func() ([]byte, error) {
		for _, p := range snaps {
			if _, drop := stale[p.path]; drop {
				continue
			}
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

// readAndMerge reads the cache file at path and merges its entries into memory.
// A missing, unreadable, oversized, corrupt or mismatched file is ignored and
// logged once. The modification time recorded here is taken before the read, so
// a write that lands during the read makes the next miss read again.
func (c *headerCache[S]) readAndMerge(path string) {
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
	// Recorded even when the file is then ignored: a corrupt file would
	// otherwise be re-read and re-logged on every miss until it changes.
	mtime := info.ModTime().UnixNano()
	c.mu.Lock()
	c.loadedMtime = mtime
	c.mu.Unlock()
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
		if e.Path == "" || !filepath.IsAbs(e.Path) {
			continue
		}
		decoded = append(decoded, loaded{path: e.Path, id: headerIdentity{size: e.Size, mtime: e.MtimeNS}, snap: snap})
	}

	// Identities are read from the model files before the lock is taken: a
	// stat per entry is cheap, but holding the lock across them is not.
	current := make(map[string]headerIdentity, len(decoded))
	for _, d := range decoded {
		if fi, err := os.Stat(d.path); err == nil {
			current[d.path] = headerIdentity{size: fi.Size(), mtime: fi.ModTime().UnixNano()}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// The file lists the most recently used entry first, so entries are merged
	// from the back: the most recent one of the file ends up nearest the front
	// of the entries this frontend does not already hold.
	for i := len(decoded) - 1; i >= 0; i-- {
		d := decoded[i]
		if el, exists := c.index[d.path]; exists {
			// Both frontends hold an entry for this model. Keep the one that
			// matches the model file on disk. If neither does, drop both. If the
			// file cannot be stat'ed, keep the one already in memory.
			e := el.Value.(*headerEntry[S])
			now, ok := current[d.path]
			switch {
			case !ok:
			case e.identity == now:
			case d.id == now:
				e.identity, e.snap = d.id, d.snap
			default:
				c.order.Remove(el)
				delete(c.index, d.path)
			}
			continue
		}
		c.insertBackLocked(d.path, d.id, d.snap)
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
