// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/mudler/xlog"
)

// ggufHeaderCacheSchema is the on-disk format version. A file written with any
// other value is ignored, so bump it when persistedGGUFHeader changes shape.
const ggufHeaderCacheSchema = 1

// ggufHeaderCacheMaxFileBytes refuses to read a cache file larger than this.
// The entry bound already caps the size a LocalAI build writes, so anything
// bigger was written by something else and is not worth decoding.
const ggufHeaderCacheMaxFileBytes = 64 << 20

// ggufHeaderSaveDebounce coalesces the inserts of a burst (a fresh start
// touching many models) into one write.
var ggufHeaderSaveDebounce = 2 * time.Second

// ggufHeaderCacheRename is os.Rename behind a variable so a test can simulate a
// failed commit and check that the previous file survives.
var ggufHeaderCacheRename = os.Rename

// persistedGGUFCache is the whole cache file: one per frontend instance.
type persistedGGUFCache struct {
	Schema int `json:"schema"`
	// Estimator identifies the estimator inputs the derived fields were computed
	// with. A mismatch means the values may differ, so the file is ignored.
	Estimator string               `json:"estimator"`
	Entries   []persistedGGUFEntry `json:"entries"`
}

type persistedGGUFEntry struct {
	Path    string              `json:"path"`
	Size    int64               `json:"size"`
	MtimeNS int64               `json:"mtime_ns"`
	Header  persistedGGUFHeader `json:"header"`
}

// persistedGGUFHeader mirrors ggufHeader. Every field is either a property of
// the file bytes or an estimator output whose inputs are fixed in code (no
// device metrics, fixed candidate contexts and offload), keyed by the estimator
// fingerprint. Nothing hardware-dependent is stored: the per-device budget is
// read at decision time from perDeviceVRAM, not from this file.
type persistedGGUFHeader struct {
	Name             string         `json:"name"`
	Architecture     string         `json:"architecture"`
	MaxContextLength int            `json:"max_context_length"`
	BOSTokenID       int64          `json:"bos_token_id"`
	EOSTokenID       int64          `json:"eos_token_id"`
	ChatTemplate     string         `json:"chat_template"`
	HasChatTemplate  bool           `json:"has_chat_template"`
	MTPLayers        uint32         `json:"mtp_layers"`
	HasMTP           bool           `json:"has_mtp"`
	TrainedContext   int            `json:"trained_context"`
	Footprints       map[int]uint64 `json:"footprints,omitempty"`
	MaxCtxFootprint  uint64         `json:"max_ctx_footprint"`
	// TrainedPanic is the estimator panic message, kept so the replay still
	// happens for this file. The replay panics with the message as a string,
	// which the defaults path recovers the same way.
	TrainedPanic *string `json:"trained_panic,omitempty"`
}

// ggufHeaderInstanceIDPattern is the set of instance IDs used verbatim in a
// file name. Any other ID is hashed, so an ID cannot carry a path separator.
var ggufHeaderInstanceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ggufHeaderCachePath returns the cache file for an instance. An empty ID
// (standalone mode) uses a fixed name.
func ggufHeaderCachePath(dataPath, instanceID string) string {
	name := "gguf-header-cache.json"
	if instanceID != "" {
		key := instanceID
		if !ggufHeaderInstanceIDPattern.MatchString(instanceID) {
			sum := sha256.Sum256([]byte(instanceID))
			key = "h-" + hex.EncodeToString(sum[:8])
		}
		name = "gguf-header-cache-" + key + ".json"
	}
	return filepath.Join(dataPath, name)
}

// ggufEstimatorFingerprint names the inputs that decide the estimator-derived
// fields: the gguf-parser version, the candidate contexts, the auto context
// size and the offload count. Changing any of them changes the file's key.
func ggufEstimatorFingerprint() string {
	parser := "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/gpustack/gguf-parser-go" {
				parser = d.Version
				if d.Replace != nil {
					parser = d.Replace.Path + "@" + d.Replace.Version
				}
			}
		}
	}
	return fmt.Sprintf("gguf-parser=%s;candidates=%v;auto=%d;ngl=%d",
		parser, contextFitCandidates, DefaultAutoContextSize, DefaultNGPULayers)
}

// attachDisk makes the cache persistent under dataPath for instanceID. It loads
// the existing file once, then starts the background saver. An empty dataPath
// keeps the cache in memory only. Calling it again replaces the previous attach.
func (c *ggufHeaderCache) attachDisk(parent context.Context, dataPath, instanceID string) {
	c.stopPersist()
	if dataPath == "" {
		return
	}
	path := ggufHeaderCachePath(dataPath, instanceID)
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
func (c *ggufHeaderCache) stopPersist() {
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
func (c *ggufHeaderCache) markDirtyLocked() {
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
func (c *ggufHeaderCache) runSaver(ctx context.Context, saveCh <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			c.flushIfDirty()
			return
		case <-saveCh:
			timer := time.NewTimer(ggufHeaderSaveDebounce)
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

func (c *ggufHeaderCache) flushIfDirty() {
	c.mu.Lock()
	dirty := c.dirty
	c.mu.Unlock()
	if !dirty {
		return
	}
	if err := c.flush(); err != nil {
		xlog.Warn("GGUF header cache: could not save, it will retry on the next change", "error", err)
	}
}

// flush writes the current cache to its file now. It returns nil and writes
// nothing when the cache is not persistent.
func (c *ggufHeaderCache) flush() error {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()

	c.mu.Lock()
	path := c.persistPath
	if path == "" {
		c.mu.Unlock()
		return nil
	}
	file := persistedGGUFCache{
		Schema:    ggufHeaderCacheSchema,
		Estimator: ggufEstimatorFingerprint(),
		Entries:   make([]persistedGGUFEntry, 0, c.order.Len()),
	}
	for el := c.order.Front(); el != nil; el = el.Next() {
		e := el.Value.(*ggufHeaderEntry)
		file.Entries = append(file.Entries, persistedGGUFEntry{
			Path:    e.path,
			Size:    e.identity.size,
			MtimeNS: e.identity.mtime,
			Header:  toPersistedHeader(e.header),
		})
	}
	c.dirty = false
	c.mu.Unlock()

	data, err := json.Marshal(file)
	if err == nil {
		err = writeFileAtomic(path, data)
	}
	if err != nil {
		c.mu.Lock()
		c.dirty = true
		c.mu.Unlock()
		return err
	}
	xlog.Debug("GGUF header cache saved", "path", path, "entries", len(file.Entries))
	return nil
}

// loadFile fills the cache from path. A missing, unreadable, oversized, corrupt
// or mismatched file is ignored and logged once. Entries are not trusted here:
// the identity check at lookup decides whether each one is used.
func (c *ggufHeaderCache) loadFile(path string) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		xlog.Debug("GGUF header cache: no file yet", "path", path)
		return
	}
	if err != nil {
		xlog.Warn("GGUF header cache: cannot stat file, starting empty", "path", path, "error", err)
		return
	}
	if info.Size() > ggufHeaderCacheMaxFileBytes {
		xlog.Warn("GGUF header cache: file too large, ignoring it", "path", path, "bytes", info.Size())
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		xlog.Warn("GGUF header cache: cannot read file, starting empty", "path", path, "error", err)
		return
	}
	var file persistedGGUFCache
	if err := json.Unmarshal(raw, &file); err != nil {
		xlog.Warn("GGUF header cache: file is corrupt, ignoring it", "path", path, "error", err)
		return
	}
	if file.Schema != ggufHeaderCacheSchema {
		xlog.Warn("GGUF header cache: unsupported schema, ignoring it", "path", path, "schema", file.Schema)
		return
	}
	if file.Estimator != ggufEstimatorFingerprint() {
		xlog.Debug("GGUF header cache: estimator inputs changed, ignoring file", "path", path)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// The file lists the most recently used entry first. Keep the first max
	// entries, then insert from the back so the first one ends at the front.
	entries := file.Entries
	if len(entries) > c.max {
		entries = entries[:c.max]
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Path == "" || !filepath.IsAbs(e.Path) {
			continue
		}
		if _, exists := c.index[e.Path]; exists {
			continue
		}
		c.insertLocked(e.Path, ggufIdentity{size: e.Size, mtime: e.MtimeNS}, fromPersistedHeader(e.Header))
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
	return ggufHeaderCacheRename(tmp.Name(), path)
}

func toPersistedHeader(h ggufHeader) persistedGGUFHeader {
	var panicked *string
	if h.trainedPanic != nil {
		msg := fmt.Sprint(h.trainedPanic)
		panicked = &msg
	}
	return persistedGGUFHeader{
		Name:             h.Name,
		Architecture:     h.Architecture,
		MaxContextLength: h.MaxContextLength,
		BOSTokenID:       h.BOSTokenID,
		EOSTokenID:       h.EOSTokenID,
		ChatTemplate:     h.ChatTemplate,
		HasChatTemplate:  h.HasChatTemplate,
		MTPLayers:        h.MTPLayers,
		HasMTP:           h.HasMTP,
		TrainedContext:   h.TrainedContext,
		Footprints:       h.footprints,
		MaxCtxFootprint:  h.maxCtxFootprint,
		TrainedPanic:     panicked,
	}
}

func fromPersistedHeader(p persistedGGUFHeader) ggufHeader {
	var panicked any
	if p.TrainedPanic != nil {
		panicked = *p.TrainedPanic
	}
	return ggufHeader{
		Name:             p.Name,
		Architecture:     p.Architecture,
		MaxContextLength: p.MaxContextLength,
		BOSTokenID:       p.BOSTokenID,
		EOSTokenID:       p.EOSTokenID,
		ChatTemplate:     p.ChatTemplate,
		HasChatTemplate:  p.HasChatTemplate,
		MTPLayers:        p.MTPLayers,
		HasMTP:           p.HasMTP,
		TrainedContext:   p.TrainedContext,
		footprints:       p.Footprints,
		maxCtxFootprint:  p.MaxCtxFootprint,
		trainedPanic:     panicked,
	}
}

// ConfigureGGUFHeaderCache attaches the process-wide header cache to the
// instance's data directory. Call it once at startup, after the instance ID is
// known and before model configs are loaded. An empty dataPath (standalone
// without data) keeps the cache in memory only. The cache is a documented
// per-instance exception: each frontend keeps its own file.
func ConfigureGGUFHeaderCache(ctx context.Context, dataPath, instanceID string) {
	ggufHeaders.attachDisk(ctx, dataPath, instanceID)
}
