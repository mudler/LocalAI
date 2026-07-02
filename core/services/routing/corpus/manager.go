// Package corpus persists and serves the labelled exemplar corpora
// behind KNN routing (router classifier "knn").
//
// The corpus FILE is the source of truth: one JSONL file per store
// name under <dir>, each line an Entry {text, labels, vector,
// embedding_model}. The local-store vector backend is a pure in-memory
// index rebuilt from the file — it has no persistence of its own (its
// Load is an explicit no-op), and keeping it that way preserves the
// documented swap point for external vector backends. Vectors are
// cached in the file alongside the text so a restart re-indexes
// without re-embedding; entries recorded under a different embedding
// model are re-embedded on load.
//
// Texts in the corpus file never leave the server: Stats exposes label
// counts only, and there is deliberately no API that returns entries.
package corpus

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/xlog"
)

// Entry is one labelled exemplar. Vector and EmbeddingModel are the
// embedding cache — absent (or stale) entries are re-embedded when the
// corpus is loaded or added to.
type Entry struct {
	Text           string    `json:"text"`
	Labels         []string  `json:"labels"`
	Vector         []float32 `json:"vector,omitempty"`
	EmbeddingModel string    `json:"embedding_model,omitempty"`
}

// Stats is the inspection surface — counts only, never texts. The
// router corpus API and the Routing tab render this.
type Stats struct {
	StoreName   string         `json:"store_name"`
	Total       int            `json:"total"`
	LabelCounts map[string]int `json:"label_counts"`
	// EmbeddingModels lists the distinct embedder fingerprints present
	// in the file. More than one entry means a re-embed is pending for
	// part of the corpus (it happens lazily on the next load).
	EmbeddingModels []string `json:"embedding_models,omitempty"`
}

// batchIndex and deleter are optional fast paths the local-store
// implementation provides; the Manager degrades to per-entry Insert
// (and to "entries persist in the index until restart" on Clear) when
// a store doesn't.
type batchIndex interface {
	InsertBatch(ctx context.Context, vecs [][]float32, payloads [][]byte) error
}

type deleter interface {
	Delete(ctx context.Context, vecs [][]float32) error
}

// Manager owns the corpus files and their sync state into the
// in-memory vector index. One per process, shared by the classifier
// build path (EnsureLoaded) and the corpus API (Add/Stats/Clear).
type Manager struct {
	dir string

	mu sync.Mutex
	// loadedModel records which embedding model a store name was synced
	// into the live index under. Guards double-loading and detects the
	// embedding-model-changed-on-a-live-index case, which local-store
	// cannot serve (it enforces one key length per store).
	loadedModel map[string]string
}

// NewManager roots corpus files at dir (created lazily on first
// write).
func NewManager(dir string) *Manager {
	return &Manager{dir: dir, loadedModel: map[string]string{}}
}

// path maps a store name to its corpus file. Store names come from
// YAML (store_name) or model names; sanitise so they can't escape the
// corpus dir or collide with path syntax.
func (m *Manager) path(storeName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, storeName)
	return filepath.Join(m.dir, safe+".jsonl")
}

// EnsureLoaded syncs the persisted corpus for storeName into the live
// vector index, once per (store, embedding model) per process. Entries
// recorded under a different embedding model are re-embedded and the
// file rewritten. Returns the number of entries now indexed. A missing
// file is an empty corpus, not an error.
func (m *Manager) EnsureLoaded(ctx context.Context, storeName, embeddingModel string, embedder backend.Embedder, store backend.VectorStore) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if prev, ok := m.loadedModel[storeName]; ok {
		if prev == embeddingModel {
			return 0, nil
		}
		// The in-memory index already holds vectors from another
		// embedder; local-store enforces a single key length, so mixing
		// would fail on insert (or silently corrupt neighbourhoods when
		// dimensions happen to match). A restart re-indexes cleanly.
		return 0, fmt.Errorf("corpus %q was indexed with embedding model %q this process; restart LocalAI to re-index it with %q", storeName, prev, embeddingModel)
	}

	entries, err := m.read(storeName)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		m.loadedModel[storeName] = embeddingModel
		return 0, nil
	}

	dirty := false
	for i := range entries {
		if len(entries[i].Vector) > 0 && entries[i].EmbeddingModel == embeddingModel {
			continue
		}
		if embedder == nil {
			return 0, fmt.Errorf("corpus %q has entries needing (re-)embedding but no embedder is available", storeName)
		}
		vec, err := embedder.Embed(ctx, entries[i].Text)
		if err != nil {
			return 0, fmt.Errorf("corpus %q: re-embedding entry %d: %w", storeName, i, err)
		}
		entries[i].Vector = vec
		entries[i].EmbeddingModel = embeddingModel
		dirty = true
	}
	if dirty {
		if err := m.write(storeName, entries); err != nil {
			return 0, err
		}
	}

	if err := insertAll(ctx, store, entries); err != nil {
		return 0, err
	}
	m.loadedModel[storeName] = embeddingModel
	return len(entries), nil
}

// Add validates, embeds, persists, and indexes new exemplars. Entries
// whose text is already in the corpus are skipped (an exemplar's
// labels are corrected via Clear + reseed, not silent overwrite).
// Returns (added, skipped). The file write happens before the index
// insert: if indexing fails the entries are still durable and the next
// EnsureLoaded (or restart) syncs them.
func (m *Manager) Add(ctx context.Context, storeName, embeddingModel string, embedder backend.Embedder, store backend.VectorStore, entries []Entry) (int, int, error) {
	if embedder == nil {
		return 0, 0, fmt.Errorf("corpus %q: no embedder available", storeName)
	}
	for i, e := range entries {
		if strings.TrimSpace(e.Text) == "" {
			return 0, 0, fmt.Errorf("corpus entry %d: empty text", i)
		}
		if len(e.Labels) == 0 {
			return 0, 0, fmt.Errorf("corpus entry %d: at least one label required", i)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, err := m.read(storeName)
	if err != nil {
		return 0, 0, err
	}
	seen := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		seen[e.Text] = struct{}{}
	}

	added := make([]Entry, 0, len(entries))
	skipped := 0
	for _, e := range entries {
		if _, dup := seen[e.Text]; dup {
			skipped++
			continue
		}
		seen[e.Text] = struct{}{}
		vec, err := embedder.Embed(ctx, e.Text)
		if err != nil {
			return 0, skipped, fmt.Errorf("corpus %q: embedding %q-labelled entry: %w", storeName, e.Labels[0], err)
		}
		e.Vector = vec
		e.EmbeddingModel = embeddingModel
		added = append(added, e)
	}
	if len(added) == 0 {
		return 0, skipped, nil
	}

	if err := m.write(storeName, append(existing, added...)); err != nil {
		return 0, skipped, err
	}
	if store != nil {
		if err := insertAll(ctx, store, added); err != nil {
			// Durable but not indexed — routing won't see the new
			// entries until the next successful load. Surface loudly.
			return len(added), skipped, fmt.Errorf("corpus %q: entries persisted but indexing failed (they will index on next load/restart): %w", storeName, err)
		}
	}
	return len(added), skipped, nil
}

// Stats reports label counts for the persisted corpus. Never returns
// entry texts.
func (m *Manager) Stats(storeName string) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := m.read(storeName)
	if err != nil {
		return Stats{}, err
	}
	s := Stats{StoreName: storeName, Total: len(entries), LabelCounts: map[string]int{}}
	models := map[string]struct{}{}
	for _, e := range entries {
		for _, l := range e.Labels {
			s.LabelCounts[l]++
		}
		if e.EmbeddingModel != "" {
			models[e.EmbeddingModel] = struct{}{}
		}
	}
	for mn := range models {
		s.EmbeddingModels = append(s.EmbeddingModels, mn)
	}
	sort.Strings(s.EmbeddingModels)
	return s, nil
}

// Clear deletes the corpus file and removes its vectors from the live
// index when the store supports deletion. When it doesn't, the index
// keeps serving stale entries until restart — the returned count and
// the warning log make that visible rather than silent.
func (m *Manager) Clear(ctx context.Context, storeName string, store backend.VectorStore) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := m.read(storeName)
	if err != nil {
		return 0, err
	}
	if err := os.Remove(m.path(storeName)); err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	delete(m.loadedModel, storeName)
	if len(entries) == 0 || store == nil {
		return len(entries), nil
	}
	if d, ok := store.(deleter); ok {
		vecs := make([][]float32, 0, len(entries))
		for _, e := range entries {
			if len(e.Vector) > 0 {
				vecs = append(vecs, e.Vector)
			}
		}
		if len(vecs) > 0 {
			if err := d.Delete(ctx, vecs); err != nil {
				return len(entries), fmt.Errorf("corpus %q: file cleared but live index deletion failed (stale entries served until restart): %w", storeName, err)
			}
		}
	} else {
		xlog.Warn("corpus: vector store does not support deletion; cleared corpus stays in the live index until restart",
			"store", storeName, "entries", len(entries))
	}
	return len(entries), nil
}

func insertAll(ctx context.Context, store backend.VectorStore, entries []Entry) error {
	vecs := make([][]float32, 0, len(entries))
	payloads := make([][]byte, 0, len(entries))
	for _, e := range entries {
		payload, err := router.EncodeCorpusEntry(e.Labels)
		if err != nil {
			return err
		}
		vecs = append(vecs, e.Vector)
		payloads = append(payloads, payload)
	}
	if b, ok := store.(batchIndex); ok {
		return b.InsertBatch(ctx, vecs, payloads)
	}
	for i := range vecs {
		if err := store.Insert(ctx, vecs[i], payloads[i]); err != nil {
			return err
		}
	}
	return nil
}

// read returns the persisted entries for storeName; a missing file is
// an empty corpus. Callers hold m.mu.
func (m *Manager) read(storeName string) ([]Entry, error) {
	f, err := os.Open(m.path(storeName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var entries []Entry
	sc := bufio.NewScanner(f)
	// Vectors inline in JSON push line length well past the default
	// 64KiB scanner cap (a 4096-dim float32 vector is ~50KiB of JSON
	// alone). 16MiB bounds any realistic embedding width.
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, fmt.Errorf("corpus file %s line %d: %w", m.path(storeName), line, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// write atomically replaces the corpus file (tmp + rename) so a crash
// mid-write can't truncate the corpus. Callers hold m.mu.
func (m *Manager) write(storeName string, entries []Entry) error {
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		return err
	}
	target := m.path(storeName)
	tmp, err := os.CreateTemp(m.dir, filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
