package vram

import (
	"context"
	"sync"
)

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

type sizeCacheEntry struct {
	size       int64
	err        error
	generation uint64
}

type cachedSizeResolver struct {
	underlying SizeResolver
	mu         sync.Mutex
	cache      map[string]sizeCacheEntry
}

func (c *cachedSizeResolver) ContentLength(ctx context.Context, uri string) (int64, error) {
	gen := currentGeneration()
	c.mu.Lock()
	e, ok := c.cache[uri]
	c.mu.Unlock()
	if ok && e.generation == gen {
		return e.size, e.err
	}
	size, err := c.underlying.ContentLength(ctx, uri)
	c.mu.Lock()
	c.cache[uri] = sizeCacheEntry{size: size, err: err, generation: gen}
	c.mu.Unlock()
	return size, err
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
}

func (c *cachedGGUFReader) ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error) {
	gen := currentGeneration()
	c.mu.Lock()
	e, ok := c.cache[uri]
	c.mu.Unlock()
	if ok && e.generation == gen {
		return e.meta, e.err
	}
	meta, err := c.underlying.ReadMetadata(ctx, uri)
	c.mu.Lock()
	c.cache[uri] = ggufCacheEntry{meta: meta, err: err, generation: gen}
	c.mu.Unlock()
	return meta, err
}

// DefaultCachedSizeResolver returns a cached SizeResolver using the default implementation.
// Entries are invalidated when the gallery generation changes.
func DefaultCachedSizeResolver() SizeResolver {
	return defaultCachedSizeResolver
}

// DefaultCachedGGUFReader returns a cached GGUFMetadataReader using the default implementation.
// Entries are invalidated when the gallery generation changes.
func DefaultCachedGGUFReader() GGUFMetadataReader {
	return defaultCachedGGUFReader
}

var (
	defaultCachedSizeResolver = &cachedSizeResolver{underlying: defaultSizeResolver{}, cache: make(map[string]sizeCacheEntry)}
	defaultCachedGGUFReader   = &cachedGGUFReader{underlying: defaultGGUFReader{}, cache: make(map[string]ggufCacheEntry)}
)
