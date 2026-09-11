package vram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const persistentCacheEntryLimit = 4096
const persistentCacheVersion = 1

var defaultPersistentGuard = &persistentGenerationGuard{}
var defaultCacheMu sync.RWMutex

// galleryGenFunc returns the current gallery generation counter.
// When set, cache entries are invalidated when the generation changes.
// When nil (e.g., in tests or non-gallery contexts), entries never expire.
var galleryGenFunc func() uint64

// SetGalleryGenerationFunc wires the gallery generation counter into the
// VRAM caches. Call this once at application startup.
func SetGalleryGenerationFunc(fn func() uint64) {
	galleryGenFunc = fn
}

func currentGeneration() uint64 {
	if galleryGenFunc != nil {
		return galleryGenFunc()
	}
	return 0
}

// ConfigurePersistentCache replaces the process-wide estimator caches with
// instances that reuse successful remote probes across server restarts.
func ConfigurePersistentCache(dir string, ttl time.Duration) {
	defaultCacheMu.Lock()
	defer defaultCacheMu.Unlock()
	removeAbandonedPersistentTemps(dir)
	prunePersistentEntries(dir, ttl, persistentCacheEntryLimit)
	guard := &persistentGenerationGuard{dir: dir}
	defaultPersistentGuard = guard
	defaultCachedSizeResolver = newCachedSizeResolverWithGuard(defaultSizeResolver{}, dir, ttl, guard)
	defaultCachedGGUFReader = newCachedGGUFReaderWithGuard(defaultGGUFReader{}, dir, ttl, guard)
}

// DisablePersistentCache keeps process-local caching but stops disk reads and writes.
func DisablePersistentCache() {
	defaultCacheMu.Lock()
	defer defaultCacheMu.Unlock()
	defaultPersistentGuard = &persistentGenerationGuard{}
	defaultCachedSizeResolver = newCachedSizeResolver(defaultSizeResolver{}, "", 0)
	defaultCachedGGUFReader = newCachedGGUFReader(defaultGGUFReader{}, "", 0)
}

// InvalidatePersistentCache removes remote probe results after the gallery
// changes, including when no estimate is requested before the next restart.
func InvalidatePersistentCache() {
	defaultCacheMu.RLock()
	guard := defaultPersistentGuard
	defaultCacheMu.RUnlock()
	guard.invalidate(currentGeneration())
}

func removeAbandonedPersistentTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), ".vram-") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func removePersistentEntries(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && (strings.HasPrefix(name, "size-") || strings.HasPrefix(name, "gguf-")) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

type persistentGenerationGuard struct {
	mu         sync.Mutex
	dir        string
	generation uint64
	set        bool
}

func (g *persistentGenerationGuard) invalidate(generation uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	removePersistentEntries(g.dir)
	g.generation = generation
	g.set = true
}

func (g *persistentGenerationGuard) canRead(generation uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.set {
		g.generation = generation
		g.set = true
		return true
	}
	if g.generation == generation {
		return true
	}
	removePersistentEntries(g.dir)
	g.generation = generation
	return false
}

func (g *persistentGenerationGuard) persist(generation uint64, write func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.set {
		g.generation = generation
		g.set = true
	}
	if g.generation == generation {
		write()
	}
}

func prunePersistentEntries(dir string, ttl time.Duration, limit int) {
	if dir == "" || ttl <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type cacheFile struct {
		path    string
		modTime time.Time
	}
	files := make([]cacheFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && (strings.HasPrefix(name, "size-") || strings.HasPrefix(name, "gguf-")) {
			if info, err := entry.Info(); err == nil {
				path := filepath.Join(dir, name)
				if time.Since(info.ModTime()) > ttl {
					_ = os.Remove(path)
					continue
				}
				files = append(files, cacheFile{path: path, modTime: info.ModTime()})
			}
		}
	}
	if limit <= 0 || len(files) <= limit {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files[:len(files)-limit] {
		_ = os.Remove(file.path)
	}
}

func persistentRemoteURI(uri string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return (scheme == "http" || scheme == "https") && parsed.Host != ""
}

type sizeCacheEntry struct {
	size       int64
	err        error
	generation uint64
}

type cachedSizeResolver struct {
	underlying SizeResolver
	mu         sync.Mutex
	cache      map[string]sizeCacheEntry
	diskDir    string
	diskTTL    time.Duration
	diskGuard  *persistentGenerationGuard
}

type persistentSizeEntry struct {
	Version int   `json:"version"`
	Size    int64 `json:"size"`
}

func newCachedSizeResolver(underlying SizeResolver, diskDir string, diskTTL time.Duration) *cachedSizeResolver {
	return newCachedSizeResolverWithGuard(underlying, diskDir, diskTTL, &persistentGenerationGuard{dir: diskDir})
}

func newCachedSizeResolverWithGuard(underlying SizeResolver, diskDir string, diskTTL time.Duration, guard *persistentGenerationGuard) *cachedSizeResolver {
	return &cachedSizeResolver{
		underlying: underlying,
		cache:      make(map[string]sizeCacheEntry),
		diskDir:    diskDir,
		diskTTL:    diskTTL,
		diskGuard:  guard,
	}
}

func (c *cachedSizeResolver) ContentLength(ctx context.Context, uri string) (int64, error) {
	gen := currentGeneration()
	c.mu.Lock()
	e, ok := c.cache[uri]
	c.mu.Unlock()
	if ok && e.generation == gen {
		return e.size, e.err
	}
	if persistentRemoteURI(uri) && c.canReadPersistent(gen) {
		if size, ok := c.readPersistent(uri); ok {
			c.mu.Lock()
			c.cache[uri] = sizeCacheEntry{size: size, generation: gen}
			c.mu.Unlock()
			return size, nil
		}
	}
	size, err := c.underlying.ContentLength(ctx, uri)
	c.mu.Lock()
	c.cache[uri] = sizeCacheEntry{size: size, err: err, generation: gen}
	c.mu.Unlock()
	if err == nil && persistentRemoteURI(uri) {
		c.writePersistent(uri, size, gen)
	}
	return size, err
}

func (c *cachedSizeResolver) canReadPersistent(generation uint64) bool {
	return c.diskGuard.canRead(generation)
}

func (c *cachedSizeResolver) persistentPath(uri string) string {
	digest := sha256.Sum256([]byte(uri))
	return filepath.Join(c.diskDir, "size-"+hex.EncodeToString(digest[:])+".json")
}

func (c *cachedSizeResolver) readPersistent(uri string) (int64, bool) {
	if c.diskDir == "" || c.diskTTL <= 0 {
		return 0, false
	}
	path := c.persistentPath(uri)
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > c.diskTTL {
		return 0, false
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is a hash under the configured cache directory.
	if err != nil {
		return 0, false
	}
	var entry persistentSizeEntry
	if json.Unmarshal(data, &entry) != nil || entry.Version != persistentCacheVersion || entry.Size <= 0 {
		return 0, false
	}
	return entry.Size, true
}

func (c *cachedSizeResolver) writePersistent(uri string, size int64, generation uint64) {
	if c.diskDir == "" || c.diskTTL <= 0 || os.MkdirAll(c.diskDir, 0o750) != nil {
		return
	}
	c.diskGuard.persist(generation, func() {
		writePersistentJSON(c.persistentPath(uri), persistentSizeEntry{Version: persistentCacheVersion, Size: size}, c.diskTTL)
	})
}

type ggufCacheEntry struct {
	meta       *GGUFMeta
	err        error
	generation uint64
}

type cachedGGUFReader struct {
	underlying GGUFMetadataReader
	mu         sync.Mutex
	cache      map[string]ggufCacheEntry
	diskDir    string
	diskTTL    time.Duration
	diskGuard  *persistentGenerationGuard
}

type persistentGGUFEntry struct {
	Version int       `json:"version"`
	Meta    *GGUFMeta `json:"meta"`
}

func newCachedGGUFReader(underlying GGUFMetadataReader, diskDir string, diskTTL time.Duration) *cachedGGUFReader {
	return newCachedGGUFReaderWithGuard(underlying, diskDir, diskTTL, &persistentGenerationGuard{dir: diskDir})
}

func newCachedGGUFReaderWithGuard(underlying GGUFMetadataReader, diskDir string, diskTTL time.Duration, guard *persistentGenerationGuard) *cachedGGUFReader {
	return &cachedGGUFReader{
		underlying: underlying,
		cache:      make(map[string]ggufCacheEntry),
		diskDir:    diskDir,
		diskTTL:    diskTTL,
		diskGuard:  guard,
	}
}

func (c *cachedGGUFReader) ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error) {
	gen := currentGeneration()
	c.mu.Lock()
	e, ok := c.cache[uri]
	c.mu.Unlock()
	if ok && e.generation == gen {
		return e.meta, e.err
	}
	if persistentRemoteURI(uri) && c.canReadPersistent(gen) {
		if meta, ok := c.readPersistent(uri); ok {
			c.mu.Lock()
			c.cache[uri] = ggufCacheEntry{meta: meta, generation: gen}
			c.mu.Unlock()
			return meta, nil
		}
	}
	meta, err := c.underlying.ReadMetadata(ctx, uri)
	c.mu.Lock()
	c.cache[uri] = ggufCacheEntry{meta: meta, err: err, generation: gen}
	c.mu.Unlock()
	if err == nil && meta != nil && persistentRemoteURI(uri) {
		c.writePersistent(uri, meta, gen)
	}
	return meta, err
}

func (c *cachedGGUFReader) canReadPersistent(generation uint64) bool {
	return c.diskGuard.canRead(generation)
}

func (c *cachedGGUFReader) persistentPath(uri string) string {
	digest := sha256.Sum256([]byte(uri))
	return filepath.Join(c.diskDir, "gguf-"+hex.EncodeToString(digest[:])+".json")
}

func (c *cachedGGUFReader) readPersistent(uri string) (*GGUFMeta, bool) {
	if c.diskDir == "" || c.diskTTL <= 0 {
		return nil, false
	}
	path := c.persistentPath(uri)
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > c.diskTTL {
		return nil, false
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is a hash under the configured cache directory.
	if err != nil {
		return nil, false
	}
	var entry persistentGGUFEntry
	if json.Unmarshal(data, &entry) != nil || entry.Version != persistentCacheVersion || !validPersistentGGUFMeta(entry.Meta) {
		return nil, false
	}
	return entry.Meta, true
}

func (c *cachedGGUFReader) writePersistent(uri string, meta *GGUFMeta, generation uint64) {
	if c.diskDir == "" || c.diskTTL <= 0 || os.MkdirAll(c.diskDir, 0o750) != nil {
		return
	}
	c.diskGuard.persist(generation, func() {
		writePersistentJSON(c.persistentPath(uri), persistentGGUFEntry{Version: persistentCacheVersion, Meta: meta}, c.diskTTL)
	})
}

func validPersistentGGUFMeta(meta *GGUFMeta) bool {
	return meta != nil && meta.BlockCount > 0 && meta.EmbeddingLength > 0 && meta.HeadCount > 0 && meta.HeadCountKV > 0
}

func writePersistentJSON(path string, value any, ttl time.Duration) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vram-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err = tmp.Close(); err != nil {
		return
	}
	if os.Rename(tmpPath, path) == nil {
		prunePersistentEntries(filepath.Dir(path), ttl, persistentCacheEntryLimit)
	}
}

// DefaultCachedSizeResolver returns a cached SizeResolver using the default implementation.
// Entries are invalidated when the gallery generation changes.
func DefaultCachedSizeResolver() SizeResolver {
	defaultCacheMu.RLock()
	defer defaultCacheMu.RUnlock()
	return defaultCachedSizeResolver
}

// DefaultCachedGGUFReader returns a cached GGUFMetadataReader using the default implementation.
// Entries are invalidated when the gallery generation changes.
func DefaultCachedGGUFReader() GGUFMetadataReader {
	defaultCacheMu.RLock()
	defer defaultCacheMu.RUnlock()
	return defaultCachedGGUFReader
}

var (
	defaultCachedSizeResolver = newCachedSizeResolver(defaultSizeResolver{}, "", 0)
	defaultCachedGGUFReader   = newCachedGGUFReader(defaultGGUFReader{}, "", 0)
)
