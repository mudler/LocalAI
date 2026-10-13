// SPDX-License-Identifier: MIT
package config

import (
	"container/list"
	"context"
	"os"
	"path/filepath"
	"sync"

	gguf "github.com/gpustack/gguf-parser-go"
)

// ggufHeaderCacheSize bounds the in-memory header cache. Each entry is a few
// hundred bytes, so this covers a large model directory while keeping the
// memory footprint trivial. Entries past the bound are evicted least recently
// used and simply re-parsed on their next use.
const ggufHeaderCacheSize = 256

// ggufParser matches gguf.ParseGGUFFile and is the seam tests use to count
// header reads.
type ggufParser func(string, ...gguf.GGUFReadOption) (*gguf.GGUFFile, error)

// ggufHeader is the set of values model defaults read from a parsed GGUF. It
// is the only thing kept between requests: the parser output (tensors, the
// full metadata map) is not. Every field is a property of the file bytes, so a
// snapshot stays valid for as long as the file identity does not change.
type ggufHeader struct {
	Name             string // general.name, falling back as the parser does
	Architecture     string // general.architecture
	MaxContextLength int    // architecture's trained maximum, 0 when absent
	BOSTokenID       int64  // tokenizer, for diagnostic logging
	EOSTokenID       int64  // tokenizer, for diagnostic logging

	ChatTemplate    string // tokenizer.chat_template
	HasChatTemplate bool

	MTPLayers uint32 // <arch>.nextn_predict_layers, when an embedded MTP head exists
	HasMTP    bool

	// TrainedContext is the estimator's default context size (0 when the
	// estimator yields nothing usable).
	TrainedContext int
	// trainedPanic is the value the estimator panicked with, if it did. The
	// defaults path has never recovered from that panic locally; it is replayed
	// at the same point so the outcome for such a file does not change.
	trainedPanic any

	// footprints holds the per-device VRAM footprint at each context that the
	// auto-context logic may ask about. It is filled only when TrainedContext
	// is positive, the same condition under which that logic runs.
	footprints map[int]uint64
	// maxCtxFootprint is the VRAM estimate at MaxContextLength used by the
	// context_size=-1 warning.
	maxCtxFootprint uint64
}

// footprintAt returns the precomputed footprint at ctx. A context that was not
// precomputed reports 0, the same "cannot confirm" answer as an estimate gap.
func (h ggufHeader) footprintAt(ctx int) uint64 {
	return h.footprints[ctx]
}

// readGGUFHeader converts a parsed file into a snapshot. It is the only place
// the parser output is consumed on the defaults path.
func readGGUFHeader(f *gguf.GGUFFile) ggufHeader {
	var h ggufHeader
	arch := f.Architecture()
	h.Name = f.Metadata().Name
	h.Architecture = arch.Architecture
	h.MaxContextLength = int(arch.MaximumContextLength)
	tok := f.Tokenizer()
	h.BOSTokenID = tok.BOSTokenID
	h.EOSTokenID = tok.EOSTokenID

	if t, ok := f.Header.MetadataKV.Get("tokenizer.chat_template"); ok {
		h.ChatTemplate = t.ValueString()
		h.HasChatTemplate = true
	}

	h.MTPLayers, h.HasMTP = HasEmbeddedMTPHead(f)

	h.TrainedContext, h.trainedPanic = safeTrainedContext(f)
	if h.TrainedContext > 0 {
		chosen := h.TrainedContext
		if chosen > DefaultAutoContextSize {
			chosen = DefaultAutoContextSize
		}
		h.footprints = map[int]uint64{chosen: estimateContextVRAM(f, chosen)}
		for _, cand := range contextFitCandidates {
			h.footprints[cand] = estimateContextVRAM(f, cand)
		}
	}

	if h.MaxContextLength > 0 {
		h.maxCtxFootprint = estimateNonUMAAt(f, h.MaxContextLength)
	}
	return h
}

// safeTrainedContext returns the estimator's default context size and, when the
// estimator panics, the panic value instead of letting it escape.
func safeTrainedContext(f *gguf.GGUFFile) (n int, panicked any) {
	defer func() {
		if r := recover(); r != nil {
			n, panicked = 0, r
		}
	}()
	return int(f.EstimateLLaMACppRun().ContextSize), nil
}

// estimateNonUMAAt returns the discrete-GPU footprint at ctx for the -1 warning.
// It returns 0 on any estimator failure, which suppresses the warning as before.
func estimateNonUMAAt(f *gguf.GGUFFile, ctx int) (used uint64) {
	defer func() {
		if r := recover(); r != nil {
			used = 0
		}
	}()
	sum := f.EstimateLLaMACppRun(gguf.WithLLaMACppContextSize(int32(ctx))).Summarize(true, 0, 0)
	if len(sum.Items) == 0 {
		return 0
	}
	for _, v := range sum.Items[0].VRAMs {
		used += uint64(v.NonUMA)
	}
	return used
}

// ggufIdentity is what a cached header is validated against. A changed size or
// modification time means the file was rewritten, so the entry is not used.
type ggufIdentity struct {
	size  int64
	mtime int64 // UnixNano
}

type ggufHeaderEntry struct {
	path     string
	identity ggufIdentity
	header   ggufHeader
}

// ggufHeaderCache is a bounded LRU keyed by absolute path. It is safe for
// concurrent use. Lookups hold the lock only for the map and list operations,
// never while a header is parsed.
type ggufHeaderCache struct {
	mu    sync.Mutex
	max   int
	order *list.List // front is most recently used; values are *ggufHeaderEntry
	index map[string]*list.Element

	// Disk persistence, set by attachDisk. Empty persistPath means memory only.
	// See gguf_header_persist.go.
	persistPath string
	dirty       bool
	saveCh      chan struct{}
	stopSaver   context.CancelFunc
	saverDone   chan struct{}
	// persistMu serializes file writes so the saver and an explicit flush never
	// race on the same temp name or rename order.
	persistMu sync.Mutex
}

func newGGUFHeaderCache(max int) *ggufHeaderCache {
	return &ggufHeaderCache{max: max, order: list.New(), index: make(map[string]*list.Element)}
}

// ggufHeaders is the process-wide header cache used by llamaCppDefaults.
var ggufHeaders = newGGUFHeaderCache(ggufHeaderCacheSize)

func (c *ggufHeaderCache) get(path string, id ggufIdentity) (ggufHeader, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.index[path]
	if !ok {
		return ggufHeader{}, false
	}
	e := el.Value.(*ggufHeaderEntry)
	if e.identity != id {
		// The file changed since it was cached. Drop the stale entry now so the
		// fresh parse replaces it rather than sitting beside it.
		c.order.Remove(el)
		delete(c.index, path)
		c.markDirtyLocked()
		return ggufHeader{}, false
	}
	c.order.MoveToFront(el)
	return e.header, true
}

// put stores a snapshot and schedules a background save when the cache is
// persisted. It never touches the disk itself, so it is safe on the request path.
func (c *ggufHeaderCache) put(path string, id ggufIdentity, h ggufHeader) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.insertLocked(path, id, h)
	c.markDirtyLocked()
}

// insertLocked adds or replaces an entry and enforces the bound. It does not
// mark the cache dirty, so loading from disk does not schedule a rewrite.
func (c *ggufHeaderCache) insertLocked(path string, id ggufIdentity, h ggufHeader) {
	if el, ok := c.index[path]; ok {
		e := el.Value.(*ggufHeaderEntry)
		e.identity, e.header = id, h
		c.order.MoveToFront(el)
		return
	}
	c.index[path] = c.order.PushFront(&ggufHeaderEntry{path: path, identity: id, header: h})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.index, oldest.Value.(*ggufHeaderEntry).path)
	}
}

func (c *ggufHeaderCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// loadGGUFHeader returns the header snapshot for modelFile, parsing it only when
// no cached snapshot matches the file's current identity. A file that cannot be
// stat'ed or parsed is never cached, so the caller sees the same error path as
// before the cache existed.
func loadGGUFHeader(modelFile string, parse ggufParser) (ggufHeader, error) {
	abs, absErr := filepath.Abs(modelFile)
	info, statErr := os.Stat(modelFile)
	if absErr != nil || statErr != nil {
		f, err := parse(modelFile, gguf.UseMMap(), gguf.SkipLargeMetadata())
		if err != nil {
			return ggufHeader{}, err
		}
		return readGGUFHeader(f), nil
	}

	// The identity is read before the parse. If the file is rewritten during the
	// parse, the entry is keyed by the older identity and the next call misses.
	id := ggufIdentity{size: info.Size(), mtime: info.ModTime().UnixNano()}
	if h, ok := ggufHeaders.get(abs, id); ok {
		return h, nil
	}

	f, err := parse(modelFile, gguf.UseMMap(), gguf.SkipLargeMetadata())
	if err != nil {
		return ggufHeader{}, err
	}
	h := readGGUFHeader(f)
	ggufHeaders.put(abs, id, h)
	return h, nil
}
