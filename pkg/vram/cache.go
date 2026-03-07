package vram

import (
	"context"
	"sync"
	"time"
)

const defaultEstimateCacheTTL = 15 * time.Minute

type sizeCacheEntry struct {
	size  int64
	err   error
	until time.Time
}

type cachedSizeResolver struct {
	underlying SizeResolver
	ttl       time.Duration
	mu        sync.Mutex
	cache     map[string]sizeCacheEntry
}

func (c *cachedSizeResolver) ContentLength(ctx context.Context, uri string) (int64, error) {
	c.mu.Lock()
	e, ok := c.cache[uri]
	c.mu.Unlock()
	if ok && time.Now().Before(e.until) {
		return e.size, e.err
	}
	size, err := c.underlying.ContentLength(ctx, uri)
	c.mu.Lock()
	if c.cache == nil {
		c.cache = make(map[string]sizeCacheEntry)
	}
	c.cache[uri] = sizeCacheEntry{size: size, err: err, until: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return size, err
}

type ggufCacheEntry struct {
	meta  *GGUFMeta
	err   error
	until time.Time
}

type cachedGGUFReader struct {
	underlying GGUFMetadataReader
	ttl       time.Duration
	mu        sync.Mutex
	cache     map[string]ggufCacheEntry
}

func (c *cachedGGUFReader) ReadMetadata(ctx context.Context, uri string) (*GGUFMeta, error) {
	c.mu.Lock()
	e, ok := c.cache[uri]
	c.mu.Unlock()
	if ok && time.Now().Before(e.until) {
		return e.meta, e.err
	}
	meta, err := c.underlying.ReadMetadata(ctx, uri)
	c.mu.Lock()
	if c.cache == nil {
		c.cache = make(map[string]ggufCacheEntry)
	}
	c.cache[uri] = ggufCacheEntry{meta: meta, err: err, until: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return meta, err
}

// CachedSizeResolver returns a SizeResolver that caches ContentLength results by URI for the given TTL.
func CachedSizeResolver(underlying SizeResolver, ttl time.Duration) SizeResolver {
	return &cachedSizeResolver{underlying: underlying, ttl: ttl, cache: make(map[string]sizeCacheEntry)}
}

// CachedGGUFReader returns a GGUFMetadataReader that caches ReadMetadata results by URI for the given TTL.
func CachedGGUFReader(underlying GGUFMetadataReader, ttl time.Duration) GGUFMetadataReader {
	return &cachedGGUFReader{underlying: underlying, ttl: ttl, cache: make(map[string]ggufCacheEntry)}
}

// DefaultCachedSizeResolver returns a cached SizeResolver using the default implementation and default TTL (15 min).
// A single shared cache is used so repeated HEAD requests for the same URI are avoided across requests.
func DefaultCachedSizeResolver() SizeResolver {
	return defaultCachedSizeResolver
}

// DefaultCachedGGUFReader returns a cached GGUFMetadataReader using the default implementation and default TTL (15 min).
// A single shared cache is used so repeated GGUF metadata fetches for the same URI are avoided across requests.
func DefaultCachedGGUFReader() GGUFMetadataReader {
	return defaultCachedGGUFReader
}

var (
	defaultCachedSizeResolver = CachedSizeResolver(defaultSizeResolver{}, defaultEstimateCacheTTL)
	defaultCachedGGUFReader   = CachedGGUFReader(defaultGGUFReader{}, defaultEstimateCacheTTL)
)
