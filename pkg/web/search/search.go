// Package search runs web searches for a model through a pluggable provider,
// with a short cache and rate limits around it.
package search

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Query is one search.
type Query struct {
	Text string
	// MaxResults is 1 to MaxResults; zero means DefaultResults.
	MaxResults int
	// Freshness is "", "day", "week", "month" or "year".
	Freshness string
	// Site limits results to one host name.
	Site string
}

// Bounds of a query.
const (
	DefaultResults = 5
	MaxResults     = 10
	MaxQueryChars  = 400
	MaxSnippet     = 300
	MaxTitle       = 200
)

// Result is one hit.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Provider runs a search.
type Provider interface {
	// Name is a short stable name: "duckduckgo", "searxng".
	Name() string
	// Host is the host that answers, for the label of the results.
	Host() string
	Search(ctx context.Context, q Query) ([]Result, error)
}

// Errors of a search.
var (
	ErrBadQuery = errors.New("the search is not valid")
	// ErrProvider means the provider did not answer with results it could
	// read: it is down, blocks this server or changed its page.
	ErrProvider    = errors.New("the search provider did not answer with results")
	ErrRateLimited = errors.New("too many web searches")
	ErrTimeout     = errors.New("the search provider did not answer in time")
)

var siteShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}\.)*[a-z0-9-]{1,63}$`)

// Normalize checks q and fills its defaults.
func Normalize(q Query) (Query, error) {
	q.Text = strings.Join(strings.Fields(q.Text), " ")
	if q.Text == "" {
		return q, fmt.Errorf("%w: query is empty", ErrBadQuery)
	}
	if len([]rune(q.Text)) > MaxQueryChars {
		return q, fmt.Errorf("%w: query is longer than %d characters", ErrBadQuery, MaxQueryChars)
	}
	switch {
	case q.MaxResults == 0:
		q.MaxResults = DefaultResults
	case q.MaxResults < 1 || q.MaxResults > MaxResults:
		return q, fmt.Errorf("%w: max_results must be 1 to %d", ErrBadQuery, MaxResults)
	}
	switch q.Freshness {
	case "", "day", "week", "month", "year":
	default:
		return q, fmt.Errorf("%w: freshness must be day, week, month or year", ErrBadQuery)
	}
	q.Site = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(q.Site)), ".")
	if q.Site != "" && !siteShape.MatchString(q.Site) {
		return q, fmt.Errorf("%w: site must be a host name such as example.com", ErrBadQuery)
	}
	return q, nil
}

// textWithSite is the query text a provider sends.
func textWithSite(q Query) string {
	if q.Site == "" {
		return q.Text
	}
	return q.Text + " site:" + q.Site
}

// Clean trims a result field to one line of at most max characters. Providers
// outside this package use it with MaxTitle and MaxSnippet.
func Clean(s string, max int) string { return clean(s, max) }

// clean trims a result field to one line of at most max characters.
func clean(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}

// Limits bound the searches of a Searcher.
type Limits struct {
	// PerUser and Global are searches per minute. Zero means the default.
	PerUser, Global int
	CacheTTL        time.Duration
	CacheEntries    int
}

// Defaults of Limits.
const (
	DefaultPerUser      = 20
	DefaultGlobal       = 60
	DefaultCacheTTL     = 5 * time.Minute
	DefaultCacheEntries = 512
)

func (l Limits) withDefaults() Limits {
	if l.PerUser <= 0 {
		l.PerUser = DefaultPerUser
	}
	if l.Global <= 0 {
		l.Global = DefaultGlobal
	}
	if l.CacheTTL <= 0 {
		l.CacheTTL = DefaultCacheTTL
	}
	if l.CacheEntries <= 0 {
		l.CacheEntries = DefaultCacheEntries
	}
	return l
}

// Searcher adds a cache and rate limits to a provider. The state is in the
// memory of this process: in a cluster each frontend has its own.
type Searcher struct {
	p   Provider
	l   Limits
	now func() time.Time

	mu      sync.Mutex
	cache   map[string]cached
	order   []string
	windows map[string][]time.Time
}

type cached struct {
	results []Result
	host    string
	at      time.Time
}

// globalKey is the window of all users together. A user ID never has the
// form of this key.
const globalKey = "\x00global"

// NewSearcher returns a Searcher over p.
func NewSearcher(p Provider, l Limits) *Searcher {
	return &Searcher{p: p, l: l.withDefaults(), now: time.Now, cache: map[string]cached{}, windows: map[string][]time.Time{}}
}

// SetClock replaces the clock, for tests.
func (s *Searcher) SetClock(now func() time.Time) { s.now = now }

// Provider is the provider of s.
func (s *Searcher) Provider() Provider { return s.p }

// Search runs q for user. An identical query within the cache time is
// answered from the cache and does not count against the limits.
func (s *Searcher) Search(ctx context.Context, user string, q Query) ([]Result, error) {
	r, _, err := s.SearchHost(ctx, user, q)
	return r, err
}

// SearchHost is Search that also returns the host that answered: the host of
// the provider, or for an Answerer (such as a Fallback) the host of the
// provider that gave the answer. A cached answer keeps its host.
func (s *Searcher) SearchHost(ctx context.Context, user string, q Query) ([]Result, string, error) {
	q, err := Normalize(q)
	if err != nil {
		return nil, "", err
	}
	key := s.p.Name() + "\x00" + strings.ToLower(q.Text) + "\x00" + fmt.Sprint(q.MaxResults) + "\x00" + q.Freshness + "\x00" + q.Site
	if r, host, ok := s.cached(key); ok {
		return r, host, nil
	}
	if !s.admit(user) {
		return nil, "", ErrRateLimited
	}
	var results []Result
	host := s.p.Host()
	if a, ok := s.p.(Answerer); ok {
		var from Provider
		results, from, err = a.SearchFrom(ctx, q)
		if from != nil {
			host = from.Host()
		}
	} else {
		results, err = s.p.Search(ctx, q)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, "", ErrTimeout
		}
		return nil, "", err
	}
	if len(results) > q.MaxResults {
		results = results[:q.MaxResults]
	}
	s.store(key, results, host)
	return results, host, nil
}

func (s *Searcher) cached(key string) ([]Result, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[key]
	if !ok || s.now().Sub(c.at) >= s.l.CacheTTL {
		return nil, "", false
	}
	return append([]Result(nil), c.results...), c.host, true
}

func (s *Searcher) store(key string, r []Result, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cache[key]; !ok {
		s.order = append(s.order, key)
	}
	s.cache[key] = cached{results: append([]Result(nil), r...), host: host, at: s.now()}
	for len(s.order) > s.l.CacheEntries {
		delete(s.cache, s.order[0])
		s.order = s.order[1:]
	}
}

// admit counts one search for user and for everyone, in a sliding window of
// one minute, and says whether both are under their limit.
func (s *Searcher) admit(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	recent := func(k string) []time.Time {
		w := s.windows[k]
		i := 0
		for i < len(w) && now.Sub(w[i]) >= time.Minute {
			i++
		}
		w = w[i:]
		if len(w) == 0 {
			delete(s.windows, k)
		} else {
			s.windows[k] = w
		}
		return w
	}
	if len(s.windows) > 1024 {
		// Users who searched once keep a window until they search again;
		// sweep them all now and then.
		for k := range s.windows {
			recent(k)
		}
	}
	u, g := recent("u:"+user), recent(globalKey)
	if len(u) >= s.l.PerUser || len(g) >= s.l.Global {
		return false
	}
	s.windows["u:"+user] = append(u, now)
	s.windows[globalKey] = append(g, now)
	return true
}
