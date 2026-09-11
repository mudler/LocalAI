// Package corpus persists and serves the labelled exemplar corpora
// behind KNN routing (router classifier "knn").
//
// The corpus FILE is the source of truth: one JSONL file per store
// name under <dir>, each line an Entry {text, labels, vector,
// embedding_model, embedding_fingerprint}. The local-store vector backend is
// a pure in-memory index rebuilt from the file — it has no persistence of its
// own (its Load is an explicit no-op), and keeping it that way preserves the
// documented swap point for external vector backends. Vectors are
// cached in the file alongside the text so a restart re-indexes
// without re-embedding; entries recorded under a different embedding
// fingerprint are re-embedded on load.
//
// Texts in the corpus file never leave the server: Stats exposes label
// counts only, and there is deliberately no API that returns entries.
package corpus

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/xlog"
)

// Entry is one labelled exemplar. Vector, EmbeddingModel, and
// EmbeddingFingerprint are the embedding cache — absent (or stale) entries
// are re-embedded when the corpus is loaded or added to.
type Entry struct {
	Text                 string    `json:"text"`
	Labels               []string  `json:"labels"`
	Vector               []float32 `json:"vector,omitempty"`
	EmbeddingModel       string    `json:"embedding_model,omitempty"`
	EmbeddingFingerprint string    `json:"embedding_fingerprint,omitempty"`
}

// ErrLiveEmbeddingMismatch means a store already contains vectors from a
// different embedding space. Querying it with the new embedder can silently
// misroute when the dimensions happen to match, so callers must fail closed
// until LocalAI restarts with an empty live index.
var ErrLiveEmbeddingMismatch = errors.New("live corpus index uses a different embedding fingerprint")

// Stats is the inspection surface — counts only, never texts. The
// router corpus API and the Routing tab render this.
type Stats struct {
	StoreName   string         `json:"store_name"`
	Total       int            `json:"total"`
	LabelCounts map[string]int `json:"label_counts"`
	// EmbeddingModels lists the distinct embedding model names present
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
	// states records both embedding-space identity and which persisted
	// file version reached the live index. needsSync survives a durable
	// file write followed by a transient index failure, making an exact
	// retry repair the index instead of skipping every duplicate entry.
	states map[string]storeState

	// statsCache memoises Stats per store, keyed by the corpus file's
	// stat fingerprint — the file is the source of truth, so a matching
	// fingerprint means the counts are current without re-parsing the
	// (vector-laden) JSONL. Its own mutex, NOT m.mu: the status page
	// polls Stats every few seconds and must not block behind a seed
	// that is busy embedding under m.mu.
	statsMu    sync.Mutex
	statsCache map[string]cachedStats
}

type storeState struct {
	embeddingFingerprint string
	syncedFile           fileFingerprint
	needsSync            bool
	indexedEntries       int
}

type cachedStats struct {
	key   fileFingerprint
	stats Stats
}

// fileFingerprint identifies a corpus file version cheaply (one
// os.Stat). Size changes on every append and rename replaces the
// inode's mtime, so any successful write bumps the fingerprint.
type fileFingerprint struct {
	exists  bool
	size    int64
	modTime time.Time
}

// NewManager roots corpus files at dir (created lazily on first
// write).
func NewManager(dir string) *Manager {
	return &Manager{
		dir:        dir,
		states:     map[string]storeState{},
		statsCache: map[string]cachedStats{},
	}
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
	safe = strings.Trim(safe, ".")
	if safe == "" {
		safe = "store"
	}
	if len(safe) > 80 {
		safe = safe[:80]
	}
	sum := sha256.Sum256([]byte(storeName))
	return filepath.Join(m.dir, safe+"-"+hex.EncodeToString(sum[:16])+".jsonl")
}

// EnsureLoaded syncs the persisted corpus for storeName into the live
// vector index, once per persisted file version and embedding fingerprint.
// Entries recorded under a different fingerprint are re-embedded and the
// file rewritten. Returns the number of entries now indexed. A missing
// file is an empty corpus, not an error.
func (m *Manager) EnsureLoaded(ctx context.Context, storeName, embeddingModel, embeddingFingerprint string, embedder backend.Embedder, store backend.VectorStore) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if embeddingFingerprint == "" {
		return 0, fmt.Errorf("corpus %q: empty embedding fingerprint", storeName)
	}
	fileKey := m.fingerprint(storeName)
	if state, ok := m.states[storeName]; ok {
		if state.embeddingFingerprint != embeddingFingerprint {
			if state.indexedEntries > 0 || state.needsSync {
				return 0, fmt.Errorf("corpus %q %w; restart LocalAI to rebuild it", storeName, ErrLiveEmbeddingMismatch)
			}
			delete(m.states, storeName)
		} else if !state.needsSync && state.syncedFile.equal(fileKey) {
			return 0, nil
		}
	}

	entries, _, err := m.readAll(storeName)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		m.states[storeName] = storeState{
			embeddingFingerprint: embeddingFingerprint,
			syncedFile:           fileKey,
		}
		return 0, nil
	}

	dirty := false
	for i := range entries {
		if len(entries[i].Vector) > 0 && entries[i].EmbeddingFingerprint == embeddingFingerprint {
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
		entries[i].EmbeddingFingerprint = embeddingFingerprint
		dirty = true
	}
	if dirty {
		if err := m.write(storeName, entries); err != nil {
			return 0, err
		}
		fileKey = m.fingerprint(storeName)
	}

	if store == nil {
		m.states[storeName] = storeState{embeddingFingerprint: embeddingFingerprint, needsSync: true, indexedEntries: len(entries)}
		return 0, fmt.Errorf("corpus %q: no vector store available", storeName)
	}
	if err := insertAll(ctx, store, entries); err != nil {
		m.states[storeName] = storeState{embeddingFingerprint: embeddingFingerprint, needsSync: true, indexedEntries: len(entries)}
		return 0, err
	}
	m.states[storeName] = storeState{
		embeddingFingerprint: embeddingFingerprint,
		syncedFile:           fileKey,
		indexedEntries:       len(entries),
	}
	return len(entries), nil
}

// Add validates, embeds, persists, and indexes new exemplars. Entries
// whose text is already in the corpus are skipped (an exemplar's
// labels are corrected via Clear + reseed, not silent overwrite).
// Returns (added, skipped). The file write happens before the index
// insert: if indexing fails the entries are still durable and the next
// EnsureLoaded (or restart) syncs them.
//
// The embedding calls — the slow part of a big seed — run OUTSIDE
// m.mu, so a long seed doesn't freeze EnsureLoaded and other stores'
// corpus operations for its whole duration. The commit re-checks for
// duplicates under the lock, so a racing Add can at worst turn an
// entry into a skip, never a double insert.
func (m *Manager) Add(ctx context.Context, storeName, embeddingModel, embeddingFingerprint string, embedder backend.Embedder, store backend.VectorStore, entries []Entry) (int, int, error) {
	if embedder == nil {
		return 0, 0, fmt.Errorf("corpus %q: no embedder available", storeName)
	}
	if embeddingFingerprint == "" {
		return 0, 0, fmt.Errorf("corpus %q: empty embedding fingerprint", storeName)
	}
	for i, e := range entries {
		if err := validateEntry(e); err != nil {
			return 0, 0, fmt.Errorf("corpus entry %d: %w", i, err)
		}
	}

	// First pass: dedupe against the persisted corpus (and within the
	// request) so known texts aren't re-embedded.
	m.mu.Lock()
	existing, _, err := m.readAll(storeName)
	if err != nil {
		m.mu.Unlock()
		return 0, 0, err
	}
	if state, ok := m.states[storeName]; ok && state.embeddingFingerprint != embeddingFingerprint && (state.indexedEntries > 0 || state.needsSync) {
		m.mu.Unlock()
		return 0, 0, fmt.Errorf("corpus %q %w; restart LocalAI to rebuild it", storeName, ErrLiveEmbeddingMismatch)
	}
	for _, e := range existing {
		if e.EmbeddingFingerprint != embeddingFingerprint {
			m.mu.Unlock()
			return 0, 0, fmt.Errorf("corpus %q contains stale embedding vectors; load it before adding entries", storeName)
		}
	}
	state, stateOK := m.states[storeName]
	needsSync := len(existing) > 0 && (!stateOK || state.needsSync || !state.syncedFile.equal(m.fingerprint(storeName)))
	if needsSync {
		if store == nil {
			m.states[storeName] = storeState{embeddingFingerprint: embeddingFingerprint, needsSync: true, indexedEntries: len(existing)}
			m.mu.Unlock()
			return 0, 0, fmt.Errorf("corpus %q: entries persisted but no vector store is available", storeName)
		}
		if err := insertAll(ctx, store, existing); err != nil {
			m.states[storeName] = storeState{embeddingFingerprint: embeddingFingerprint, needsSync: true, indexedEntries: len(existing)}
			m.mu.Unlock()
			return 0, 0, fmt.Errorf("corpus %q: retrying live index sync: %w", storeName, err)
		}
		m.states[storeName] = storeState{
			embeddingFingerprint: embeddingFingerprint,
			syncedFile:           m.fingerprint(storeName),
			indexedEntries:       len(existing),
		}
	}
	seen := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		seen[e.Text] = struct{}{}
	}
	candidates := make([]Entry, 0, len(entries))
	skipped := 0
	for _, e := range entries {
		if _, dup := seen[e.Text]; dup {
			skipped++
			continue
		}
		seen[e.Text] = struct{}{}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		m.mu.Unlock()
		return 0, skipped, nil
	}
	m.mu.Unlock()

	for i := range candidates {
		vec, err := embedder.Embed(ctx, candidates[i].Text)
		if err != nil {
			return 0, skipped, fmt.Errorf("corpus %q: embedding %q-labelled entry: %w", storeName, candidates[i].Labels[0], err)
		}
		candidates[i].Vector = vec
		candidates[i].EmbeddingModel = embeddingModel
		candidates[i].EmbeddingFingerprint = embeddingFingerprint
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Commit: re-read to catch texts a concurrent Add landed while we
	// were embedding, then append only the survivors — JSONL appends
	// keep the write O(new entries), not O(corpus).
	current, torn, err := m.readAll(storeName)
	if err != nil {
		return 0, skipped, err
	}
	if state, ok := m.states[storeName]; ok && state.embeddingFingerprint != embeddingFingerprint && (state.indexedEntries > 0 || state.needsSync) {
		return 0, skipped, fmt.Errorf("corpus %q %w; restart LocalAI to rebuild it", storeName, ErrLiveEmbeddingMismatch)
	}
	for _, e := range current {
		if e.EmbeddingFingerprint != embeddingFingerprint {
			return 0, skipped, fmt.Errorf("corpus %q contains stale embedding vectors; load it before adding entries", storeName)
		}
	}
	seen = make(map[string]struct{}, len(current))
	for _, e := range current {
		seen[e.Text] = struct{}{}
	}
	added := candidates[:0]
	for _, e := range candidates {
		if _, dup := seen[e.Text]; dup {
			skipped++
			continue
		}
		added = append(added, e)
	}
	if len(added) == 0 {
		return 0, skipped, nil
	}

	if torn {
		// A crash mid-append left a torn final line; appending after it
		// would bury the damage mid-file. Repair with a full atomic
		// rewrite of the readable entries plus the new ones.
		err = m.write(storeName, append(current, added...))
	} else {
		err = m.appendEntries(storeName, added)
	}
	if err != nil {
		return 0, skipped, err
	}
	entryCount := len(current) + len(added)
	if store != nil {
		if err := insertAll(ctx, store, added); err != nil {
			// Durable but not indexed — routing won't see the new
			// entries until the next successful load. Surface loudly.
			m.states[storeName] = storeState{embeddingFingerprint: embeddingFingerprint, needsSync: true, indexedEntries: entryCount}
			return len(added), skipped, fmt.Errorf("corpus %q: entries persisted but indexing failed (retry the same seed request or restart): %w", storeName, err)
		}
		m.states[storeName] = storeState{
			embeddingFingerprint: embeddingFingerprint,
			syncedFile:           m.fingerprint(storeName),
			indexedEntries:       entryCount,
		}
	} else {
		m.states[storeName] = storeState{embeddingFingerprint: embeddingFingerprint, needsSync: true, indexedEntries: entryCount}
	}
	return len(added), skipped, nil
}

// Stats reports label counts for the persisted corpus. Never returns
// entry texts.
//
// Deliberately does NOT take m.mu — the status page polls this every
// few seconds and must not block behind a seed or load. Reading the
// file without the write lock is safe: write() replaces it atomically
// (rename) and appendEntries only extends the tail, whose worst-case
// torn line readAll tolerates. The fingerprint is taken BEFORE the
// read so a write racing the read at worst caches fresher stats under
// an older key, which the next call corrects.
func (m *Manager) Stats(storeName string) (Stats, error) {
	key := m.fingerprint(storeName)
	m.statsMu.Lock()
	if c, ok := m.statsCache[storeName]; ok && c.key.equal(key) {
		m.statsMu.Unlock()
		return c.stats.copy(), nil
	}
	m.statsMu.Unlock()

	entries, _, err := m.readAll(storeName)
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

	m.statsMu.Lock()
	m.statsCache[storeName] = cachedStats{key: key, stats: s.copy()}
	m.statsMu.Unlock()
	return s, nil
}

func (m *Manager) fingerprint(storeName string) fileFingerprint {
	fi, err := os.Stat(m.path(storeName))
	if err != nil {
		return fileFingerprint{}
	}
	return fileFingerprint{exists: true, size: fi.Size(), modTime: fi.ModTime()}
}

func (f fileFingerprint) equal(o fileFingerprint) bool {
	return f.exists == o.exists && f.size == o.size && f.modTime.Equal(o.modTime)
}

// copy protects the cached maps/slices from mutation by callers (and
// callers from later cache updates).
func (s Stats) copy() Stats {
	out := s
	out.LabelCounts = make(map[string]int, len(s.LabelCounts))
	for k, v := range s.LabelCounts {
		out.LabelCounts[k] = v
	}
	out.EmbeddingModels = append([]string(nil), s.EmbeddingModels...)
	return out
}

// Clear deletes the corpus file and removes its vectors from the live
// index when the store supports deletion. When it doesn't, the index
// keeps serving stale entries until restart — the returned count and
// the warning log make that visible rather than silent.
func (m *Manager) Clear(ctx context.Context, storeName string, store backend.VectorStore) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, _, err := m.readAll(storeName)
	if err != nil {
		return 0, err
	}
	if err := os.Remove(m.path(storeName)); err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	delete(m.states, storeName)
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

func validateEntry(e Entry) error {
	if strings.TrimSpace(e.Text) == "" {
		return errors.New("empty text")
	}
	if len(e.Labels) == 0 {
		return errors.New("at least one label required")
	}
	seen := make(map[string]struct{}, len(e.Labels))
	for _, label := range e.Labels {
		if strings.TrimSpace(label) == "" {
			return errors.New("labels must not be empty")
		}
		if _, ok := seen[label]; ok {
			return fmt.Errorf("duplicate label %q", label)
		}
		seen[label] = struct{}{}
	}
	return nil
}

func insertAll(ctx context.Context, store backend.VectorStore, entries []Entry) error {
	vecs := make([][]float32, 0, len(entries))
	payloads := make([][]byte, 0, len(entries))
	for _, e := range entries {
		payload, err := router.EncodeCorpusEntry(router.EntryID(e.Text), e.Labels)
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

// readAll returns the persisted entries for storeName; a missing file
// is an empty corpus. The torn flag reports an unparseable FINAL line
// — the signature a crash mid-append (or a read racing an in-flight
// append) leaves — which is dropped rather than treated as corruption;
// Add repairs it on the next write. An unparseable line anywhere else
// is real corruption and errors.
func (m *Manager) readAll(storeName string) (entries []Entry, torn bool, err error) {
	f, err := os.Open(m.path(storeName))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	// Vectors inline in JSON push line length well past the default
	// 64KiB scanner cap (a 4096-dim float32 vector is ~50KiB of JSON
	// alone). 16MiB bounds any realistic embedding width.
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	line := 0
	var pendingErr error
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		if pendingErr != nil {
			// The failed line has a successor, so it wasn't a torn tail.
			return nil, false, pendingErr
		}
		var e Entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			pendingErr = fmt.Errorf("corpus file %s line %d: %w", m.path(storeName), line, err)
			continue
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	if pendingErr != nil {
		xlog.Warn("corpus: dropping torn final line (crash or concurrent append); repaired on next write",
			"file", m.path(storeName), "line", line)
		return entries, true, nil
	}
	return entries, false, nil
}

// write atomically replaces the corpus file (tmp + fsync + rename) so
// a crash mid-write can't truncate the corpus. Callers hold m.mu.
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// appendEntries extends the corpus file in place — JSONL's whole point
// — so Add costs O(new entries), not O(corpus). A crash mid-append
// tears at most the final line, which readAll tolerates and the next
// Add rewrite repairs. Callers hold m.mu.
func (m *Manager) appendEntries(storeName string, entries []Entry) error {
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(m.path(storeName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
