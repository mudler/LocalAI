// Package modelsearch searches for installable models across several remote
// sources (Hugging Face, and the sources that add-ons provide) behind one provider-neutral
// interface. A Searcher fans a query out to the requested providers
// concurrently and returns partial results with a per-provider status, so one
// provider being down never fails the whole search.
package modelsearch

import (
	"context"
	"encoding/json"
	"slices"
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
	// SortLatest lists the newest models first. It is the one sort that works
	// without a search text, for the providers that can list their newest
	// models (see LatestLister).
	SortLatest = "latest"
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
	// keep their own order. With SortLatest the text may be empty.
	Sort string `json:"sort,omitempty"`
	// Filters are the filter values of a request, by source and filter ID.
	// The Searcher hands each provider only its own, in Own.
	Filters map[string]map[string]string `json:"-"`
	// Own are the filter values for the provider that is called, already
	// checked against the filters the source declares.
	Own map[string]string `json:"-"`
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

	// The fields below are not set by a provider. The REST route and the
	// assistant tool fill them from this server's state after the search
	// (and after the cache, since they change).

	// Installed is true when an installed model comes from this repository;
	// InstalledName is that model's name.
	Installed     bool   `json:"installed,omitempty"`
	InstalledName string `json:"installed_name,omitempty"`
	// Backend is the backend an import would likely pick, derived from the
	// tags; a hint. RepoFiles.Backend is exact.
	Backend string `json:"backend,omitempty"`
	// HasVariants is true when opening the model is likely to list several
	// quantizations: a GGUF repository, or any Pirate Face one (the source of an add-on).
	HasVariants bool `json:"has_variants,omitempty"`
}

// ProviderStatus reports how one provider did.
type ProviderStatus struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Count int    `json:"count"`
	// Cached is true when the results came from the cache.
	Cached bool `json:"cached,omitempty"`
	// Unsupported is true when the provider was not asked because it cannot
	// answer this kind of request: a list of the newest models with no search
	// text. The provider is ok and has no results; nothing went upstream.
	Unsupported bool `json:"unsupported,omitempty"`
	// Title is the name of the source, when it has one.
	Title string `json:"title,omitempty"`
	// Dropped counts the rows that the host refused: a scheme it does not
	// handle, or a field over its cap.
	Dropped int `json:"dropped,omitempty"`
}

// Response is the outcome of a search.
type Response struct {
	Results   []Result         `json:"results"`
	Providers []ProviderStatus `json:"providers"`
	// Sources describes the sources that were asked, with their filters, so a
	// client needs no second call to draw the controls.
	Sources []Source `json:"sources,omitempty"`
}

// Provider is one remote source of models.
type Provider interface {
	// Name is the stable lower-case id used in requests ("huggingface").
	Name() string
	Search(ctx context.Context, q Query) ([]Result, error)
}

// countingProvider is implemented by a provider that refuses some rows and
// says how many.
type countingProvider interface {
	SearchCounted(ctx context.Context, q Query) ([]Result, int, error)
}

// LatestLister is implemented by a provider that can list its newest models
// when the query has no text. A provider that does not implement it is never
// called for such a query: its status says unsupported.
type LatestLister interface {
	ListsLatest() bool
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
	dropped int
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

// Unregister removes a provider. It reports whether there was one.
func (s *Searcher) Unregister(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[name]; !ok {
		return false
	}
	delete(s.providers, name)
	s.order = slices.DeleteFunc(s.order, func(n string) bool { return n == name })
	return true
}

// Has reports whether a provider is registered under the name.
func (s *Searcher) Has(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.providers[name]
	return ok
}

// Source describes the provider called name. A provider that does not
// describe itself gets its name as title.
func (s *Searcher) Source(name string) (Source, bool) {
	s.mu.RLock()
	p, ok := s.providers[name]
	s.mu.RUnlock()
	if !ok {
		return Source{}, false
	}
	if d, ok := p.(Describer); ok {
		return d.Source(), true
	}
	return Source{ID: name, Title: name, Kind: "builtin", Running: true}, true
}

// Sources describes the registered providers in registration order.
func (s *Searcher) Sources() []Source {
	var out []Source
	for _, n := range s.Names() {
		if src, ok := s.Source(n); ok {
			out = append(out, src)
		}
	}
	return out
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
	case SortLikes, SortUpdated, SortTrending, SortLatest:
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
	sources := make([]*Source, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		s.mu.RLock()
		p, ok := s.providers[name]
		s.mu.RUnlock()
		if !ok {
			statuses[i] = ProviderStatus{Name: name, Error: "unknown provider"}
			continue
		}
		pq := q
		pq.Own = nil
		if src, ok := s.Source(name); ok {
			sources[i] = &src
		}
		if d, ok := p.(Describer); ok {
			src := d.Source()
			statuses[i].Title = src.Title
			own, err := ValidateFilters(src, q.Filters[name])
			if err != nil {
				statuses[i] = ProviderStatus{Name: name, Title: src.Title, Error: err.Error()}
				continue
			}
			pq.Own = withDefaults(src, own)
		}
		if q.Text == "" && !listsLatest(p, q) {
			// Nothing to search for and no way to list the newest: do not
			// send the provider a request its site has no page for.
			statuses[i] = ProviderStatus{Name: name, Title: statuses[i].Title, OK: true, Unsupported: true}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, dropped, cached, err := s.searchOne(ctx, p, pq)
			st := ProviderStatus{Name: name, Title: statuses[i].Title, OK: err == nil, Count: len(res), Cached: cached, Dropped: dropped}
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
	for _, src := range sources {
		if src != nil {
			out.Sources = append(out.Sources, *src)
		}
	}
	return out
}

// listsLatest reports whether p can answer a query with no text, which only
// the newest-first list can be.
func listsLatest(p Provider, q Query) bool {
	l, ok := p.(LatestLister)
	return ok && q.Sort == SortLatest && l.ListsLatest()
}

func (s *Searcher) searchOne(ctx context.Context, p Provider, q Query) ([]Result, int, bool, error) {
	key := cacheKey(p.Name(), q)
	if res, dropped, ok := s.cacheGet(key); ok {
		return res, dropped, true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var res []Result
	var dropped int
	var err error
	if cp, ok := p.(countingProvider); ok {
		res, dropped, err = cp.SearchCounted(ctx, q)
	} else {
		res, err = p.Search(ctx, q)
	}
	if err != nil {
		return nil, 0, false, err
	}
	if len(res) > q.Limit {
		res = res[:q.Limit]
	}
	for i := range res {
		res[i].Provider = p.Name()
	}
	s.cachePut(key, res, dropped)
	return res, dropped, false, nil
}

func cacheKey(name string, q Query) string {
	b, _ := json.Marshal([]string{name, strings.ToLower(q.Text), q.Format, q.Sort, strconv.Itoa(q.Limit), filtersKey(q.Own)})
	return string(b)
}

func (s *Searcher) cacheGet(key string) ([]Result, int, bool) {
	if s.ttl < 0 {
		return nil, 0, false
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	e, ok := s.cache[key]
	if !ok || !s.now().Before(e.expires) {
		delete(s.cache, key)
		return nil, 0, false
	}
	return append([]Result(nil), e.results...), e.dropped, true
}

func (s *Searcher) cachePut(key string, res []Result, dropped int) {
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
	s.cache[key] = cacheEntry{results: append([]Result(nil), res...), dropped: dropped, expires: now.Add(s.ttl)}
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

// PirateFaceName is the ID of the Pirate Face source. The search of that source
// is an add-on (addons/pirateface-search); the host keeps the ID because the
// importer, the file listing and the stored choice of a person name it.
const PirateFaceName = "pirateface"

// Default returns a Searcher with the built-in provider (Hugging Face). The
// other sources come from add-ons (see AddonSync). hfToken is optional and only
// raises the Hugging Face rate limit.
func Default(hfToken string) *Searcher {
	s := NewSearcher(Options{})
	s.Register(NewHuggingFace(HuggingFaceOptions{Token: hfToken}))
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
