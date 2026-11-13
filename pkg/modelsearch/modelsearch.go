// Package modelsearch searches for installable models across several remote
// providers (Hugging Face, Pirate Face, ...) behind one provider-neutral
// interface. A Searcher fans a query out to the requested providers
// concurrently and returns partial results with a per-provider status, so one
// provider being down never fails the whole search.
package modelsearch

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultLimit is the number of results asked from each provider when
	// the query sets none.
	DefaultLimit = 20
	// MaxLimit caps Query.Limit.
	MaxLimit = 50
	// MaxQueryLength caps the length of the search text, in bytes.
	MaxQueryLength = 200

	// DefaultFormat is the file format filter applied when none is given.
	DefaultFormat = "gguf"
	// FormatAny disables the format filter.
	FormatAny = "any"

	SortDownloads = "downloads"
	SortLikes     = "likes"
	SortUpdated   = "updated"
	SortTrending  = "trending"
)

// Query is what a provider is asked to search for. Searcher.Search
// normalizes it before it reaches a provider.
type Query struct {
	Text string `json:"q"`
	// Limit is the number of results wanted from each provider.
	Limit int `json:"limit,omitempty"`
	// Format filters by file format where the provider can (default
	// "gguf"; "any" disables the filter). Providers that cannot filter
	// ignore it.
	Format string `json:"format,omitempty"`
	// Sort is one of the Sort* constants. Providers that cannot sort
	// keep their own order.
	Sort string `json:"sort,omitempty"`
}

// Result is one model found by a provider.
type Result struct {
	Provider string `json:"provider"`
	// ID is the owner/repo name.
	ID    string `json:"id"`
	Title string `json:"title"`
	// URI is what the import route accepts: huggingface://owner/repo for
	// Hugging Face, pirateface://owner/repo for Pirate Face.
	URI string `json:"uri"`
	// Magnet is the BitTorrent magnet link, when the provider lists one.
	Magnet string `json:"magnet,omitempty"`
	// URL is a page a person can open.
	URL         string   `json:"url,omitempty"`
	SizeBytes   int64    `json:"size_bytes,omitempty"`
	Downloads   int64    `json:"downloads,omitempty"`
	Likes       int64    `json:"likes,omitempty"`
	Seeders     int64    `json:"seeders,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	PipelineTag string   `json:"pipeline_tag,omitempty"`
	License     string   `json:"license,omitempty"`
	// Gated is true when the provider says access needs approval. It is
	// reported as is; nothing here tries to get around it.
	Gated        bool   `json:"gated,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

// ProviderStatus reports how one provider did.
type ProviderStatus struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Count int    `json:"count"`
	// Cached is true when the results came from the cache.
	Cached bool `json:"cached,omitempty"`
}

// Response is the outcome of a search.
type Response struct {
	Results   []Result         `json:"results"`
	Providers []ProviderStatus `json:"providers"`
}

// Provider is one remote source of models.
type Provider interface {
	// Name is the stable lower-case id used in requests ("huggingface").
	Name() string
	Search(ctx context.Context, q Query) ([]Result, error)
}

// Options configures a Searcher.
type Options struct {
	// Timeout bounds each provider call. Default 15s.
	Timeout time.Duration
	// CacheTTL is how long a successful provider answer is reused.
	// Default 5 minutes; a negative value disables the cache.
	CacheTTL time.Duration
	// CacheSize is the maximum number of cached answers. Default 256.
	CacheSize int
}

// Searcher holds the registered providers and a small answer cache. The
// cache lives in this process only: every frontend of a cluster keeps its
// own, which only costs an extra upstream call.
type Searcher struct {
	timeout time.Duration
	ttl     time.Duration
	size    int
	now     func() time.Time

	mu        sync.RWMutex
	providers map[string]Provider
	order     []string

	cmu   sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	results []Result
	expires time.Time
}

// NewSearcher returns a Searcher with no providers.
func NewSearcher(o Options) *Searcher {
	if o.Timeout <= 0 {
		o.Timeout = 15 * time.Second
	}
	if o.CacheTTL == 0 {
		o.CacheTTL = 5 * time.Minute
	}
	if o.CacheSize <= 0 {
		o.CacheSize = 256
	}
	return &Searcher{
		timeout:   o.Timeout,
		ttl:       o.CacheTTL,
		size:      o.CacheSize,
		now:       time.Now,
		providers: map[string]Provider{},
		cache:     map[string]cacheEntry{},
	}
}

// Register adds a provider, replacing one with the same name.
func (s *Searcher) Register(p Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := p.Name()
	if _, ok := s.providers[name]; !ok {
		s.order = append(s.order, name)
	}
	s.providers[name] = p
}

// Names lists the registered providers in registration order.
func (s *Searcher) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.order...)
}

// Normalize applies the defaults and caps of a Query.
func (q Query) Normalize() Query {
	q.Text = strings.TrimSpace(q.Text)
	if len(q.Text) > MaxQueryLength {
		q.Text = q.Text[:MaxQueryLength]
	}
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}
	q.Format = strings.ToLower(strings.TrimSpace(q.Format))
	if q.Format == "" {
		q.Format = DefaultFormat
	}
	q.Sort = strings.ToLower(strings.TrimSpace(q.Sort))
	switch q.Sort {
	case SortLikes, SortUpdated, SortTrending:
	default:
		q.Sort = SortDownloads
	}
	return q
}

// Search queries the named providers (all of them when names is empty)
// concurrently. Results are grouped by provider in the order of names and
// keep each provider's own order. A provider that fails or times out gets
// ok=false and an error in its status; the others still answer.
func (s *Searcher) Search(ctx context.Context, q Query, names []string) Response {
	q = q.Normalize()
	if len(names) == 0 {
		names = s.Names()
	}
	names = dedupe(names)

	statuses := make([]ProviderStatus, len(names))
	results := make([][]Result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		s.mu.RLock()
		p, ok := s.providers[name]
		s.mu.RUnlock()
		if !ok {
			statuses[i] = ProviderStatus{Name: name, Error: "unknown provider"}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, cached, err := s.searchOne(ctx, p, q)
			st := ProviderStatus{Name: name, OK: err == nil, Count: len(res), Cached: cached}
			if err != nil {
				st.Error = err.Error()
			}
			statuses[i], results[i] = st, res
		}()
	}
	wg.Wait()

	out := Response{Results: []Result{}, Providers: statuses}
	for _, r := range results {
		out.Results = append(out.Results, r...)
	}
	return out
}

func (s *Searcher) searchOne(ctx context.Context, p Provider, q Query) ([]Result, bool, error) {
	key := cacheKey(p.Name(), q)
	if res, ok := s.cacheGet(key); ok {
		return res, true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	res, err := p.Search(ctx, q)
	if err != nil {
		return nil, false, err
	}
	if len(res) > q.Limit {
		res = res[:q.Limit]
	}
	for i := range res {
		res[i].Provider = p.Name()
	}
	s.cachePut(key, res)
	return res, false, nil
}

func cacheKey(name string, q Query) string {
	return strings.Join([]string{name, strings.ToLower(q.Text), q.Format, q.Sort, strconv.Itoa(q.Limit)}, "\x00")
}

func (s *Searcher) cacheGet(key string) ([]Result, bool) {
	if s.ttl < 0 {
		return nil, false
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	e, ok := s.cache[key]
	if !ok || !s.now().Before(e.expires) {
		delete(s.cache, key)
		return nil, false
	}
	return append([]Result(nil), e.results...), true
}

func (s *Searcher) cachePut(key string, res []Result) {
	if s.ttl < 0 {
		return
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	now := s.now()
	if len(s.cache) >= s.size {
		for k, e := range s.cache {
			if !now.Before(e.expires) {
				delete(s.cache, k)
			}
		}
	}
	if len(s.cache) >= s.size {
		// Still full: drop the entry closest to expiry.
		keys := make([]string, 0, len(s.cache))
		for k := range s.cache {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return s.cache[keys[i]].expires.Before(s.cache[keys[j]].expires) })
		delete(s.cache, keys[0])
	}
	s.cache[key] = cacheEntry{results: append([]Result(nil), res...), expires: now.Add(s.ttl)}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, n := range in {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// ParseNames splits a comma separated provider list.
func ParseNames(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	return dedupe(strings.Split(csv, ","))
}

// Default returns a Searcher with the built-in providers. hfToken is
// optional and only raises the Hugging Face rate limit.
func Default(hfToken string) *Searcher {
	s := NewSearcher(Options{})
	s.Register(NewHuggingFace(HuggingFaceOptions{Token: hfToken}))
	s.Register(NewPirateFace(PirateFaceOptions{}))
	return s
}

var (
	sharedOnce sync.Once
	shared     *Searcher
)

// Shared returns the process-wide Searcher with the built-in providers, so
// the REST route and the assistant tools share one cache. LocalAI has no
// Hugging Face token setting, so it searches without one and the public
// rate limit applies.
func Shared() *Searcher {
	sharedOnce.Do(func() { shared = Default("") })
	return shared
}
