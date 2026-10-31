package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/search"
	"github.com/mudler/LocalAI/pkg/web/webtools"
	"github.com/mudler/xlog"
)

// webSettingsHint is where an administrator fixes the search. It is part of
// the error a user reads when no provider works, after what each provider did.
const webSettingsHint = "an administrator can set a SearXNG instance in Settings > Tools, or install the Web add-on for search providers with a key from Add-ons"

// studioWeb serves the built-in web tools of the chats. It reads the runtime
// settings at each call and builds the fetch client and the searcher again
// when they change. The cache and the rate limits of the searcher are in the
// memory of this process: in a cluster each frontend has its own.
type studioWeb struct {
	cfg *config.ApplicationConfig
	// self lists the addresses of this server, which web_fetch never reaches.
	self func() []netip.Prefix

	// endpoints are the pages of the keyless providers; tests replace them.
	endpoints search.KeylessEndpoints

	mu    sync.Mutex
	key   string
	tools webtools.Tools
	chain *search.Fallback
	// skipped says why a provider of the order is not in the chain.
	skipped []search.Attempt
}

func newStudioWeb(cfg *config.ApplicationConfig, self func() []netip.Prefix) *studioWeb {
	if self == nil {
		self = selfAddrs(cfg)
	}
	return &studioWeb{cfg: cfg, self: self, endpoints: search.DefaultKeylessEndpoints()}
}

// serverSelf is the one list of this server's own addresses, shared by
// web_fetch and the Sandbox proxy (egress.check sends it to the add-on).
func (a *Application) serverSelf() func() []netip.Prefix {
	a.studioSelfOnce.Do(func() { a.studioSelf = selfAddrs(a.applicationConfig) })
	return a.studioSelf
}

// selfAddrs lists the addresses of this server: its interfaces, loopback and
// the host of the external base URL. A model's request must not reach the
// server's own API through any of them: neither web_fetch nor a command in
// the Sandbox, whose proxy gets this list from egress.check.
func selfAddrs(cfg *config.ApplicationConfig) func() []netip.Prefix {
	return safehttp.SelfAddrs(func() []string {
		u, err := url.Parse(strings.TrimSpace(cfg.ExternalBaseURL))
		if err != nil || u.Hostname() == "" {
			return nil
		}
		return []string{u.Hostname()}
	})
}

// Enabled implements studio.WebTools.
func (w *studioWeb) Enabled(name string) bool {
	switch name {
	case webtools.SearchName:
		return !w.cfg.DisableChatsWebSearch
	case webtools.FetchName:
		return !w.cfg.DisableChatsWebFetch
	}
	return false
}

// current returns the tools for the settings in force.
func (w *studioWeb) current() webtools.Tools {
	searx := strings.TrimSpace(w.cfg.ChatsWebSearXNGURL)
	ports, perr := config.ParsePorts(w.cfg.ChatsWebPorts)
	if perr != nil {
		// The settings endpoint refuses a bad list; one in the file is
		// logged at load. Keep the safe default.
		ports = []int{80, 443}
	}
	order, oerr := config.ParseSearchProviders(w.cfg.ChatsWebSearchProviders, searx != "")
	if oerr != nil {
		// The settings endpoint refuses a bad list; one in the file is
		// logged at load. Keep the default order.
		order, _ = config.ParseSearchProviders("", searx != "")
	}
	key := fmt.Sprint(searx, "\x00", order, "\x00", w.cfg.ChatsWebAllowPrivate, "\x00", ports)
	w.mu.Lock()
	defer w.mu.Unlock()
	if key == w.key && w.tools.Fetch != nil {
		return w.tools
	}
	client := safehttp.New(safehttp.Policy{
		AllowPrivate: w.cfg.ChatsWebAllowPrivate,
		Ports:        ports,
		DenyAddrs:    w.self,
		UserAgent:    "LocalAI/" + internal.PrintableVersion() + " (web_fetch; +https://localai.io)",
	}, nil)
	var chain []search.Provider
	var skipped []search.Attempt
	for _, n := range order {
		if n != config.SearchProviderSearXNG {
			// The keyless pages are reached through the guarded client: their
			// addresses are fixed here, the redirects they answer with are not.
			p, err := search.NewKeyless(n, client, w.endpoints)
			if err == nil {
				chain = append(chain, p)
			}
			continue
		}
		if searx == "" {
			skipped = append(skipped, search.Attempt{Provider: n, Outcome: search.OutcomeError, Reason: "no SearXNG address is set"})
			continue
		}
		p, err := search.NewSearXNG(searx)
		if err != nil {
			xlog.Warn("chats: the SearXNG URL is not valid; it is left out of web search", "error", err)
			skipped = append(skipped, search.Attempt{Provider: n, Outcome: search.OutcomeError, Reason: "the SearXNG address is not valid"})
			continue
		}
		chain = append(chain, p)
	}
	t := webtools.Tools{Fetch: client, SettingsHint: webSettingsHint}
	w.chain = nil
	if len(chain) > 0 {
		// A provider that answers with a bot check or refuses this server is
		// skipped for a few minutes. The cool-down is in the memory of this
		// process, like the cache: in a cluster each frontend keeps its own.
		w.chain = search.NewFallback(0, chain...).WithCooldown(search.DefaultCooldown)
		t.Search = search.NewSearcher(w.chain, search.Limits{})
	}
	w.key, w.tools, w.skipped = key, t, skipped
	return t
}

// Search implements studio.WebTools.
func (w *studioWeb) Search(ctx context.Context, user string, args json.RawMessage) (string, error) {
	if !w.Enabled(webtools.SearchName) {
		return "", errors.New("web search is switched off on this server")
	}
	return w.current().WebSearch(ctx, user, args)
}

// Fetch implements studio.WebTools.
func (w *studioWeb) Fetch(ctx context.Context, args json.RawMessage) (string, error) {
	if !w.Enabled(webtools.FetchName) {
		return "", errors.New("reading web pages is switched off on this server")
	}
	return w.current().WebFetch(ctx, args)
}

// TestSearch runs the query with each provider of the saved order, for the
// Test search button of the settings, and returns what each did. The first
// answer in order is what a user would get, with the provider and host that
// gave it; when none answers, err is the error a user would read. The test
// counts as one search against the limits, outside the cache.
func (w *studioWeb) TestSearch(ctx context.Context, user, query string) (provider, host string, results []search.Result, attempts []search.Attempt, err error) {
	t := w.current()
	w.mu.Lock()
	chain, skipped := w.chain, append([]search.Attempt(nil), w.skipped...)
	w.mu.Unlock()
	if t.Search == nil || chain == nil {
		return "", "", nil, skipped, t.SearchError(fmt.Errorf("%w: %s", search.ErrProvider, joinReasons(skipped)))
	}
	q, err := search.Normalize(search.Query{Text: query, MaxResults: 3})
	if err != nil {
		return "", "", nil, skipped, t.SearchError(err)
	}
	if !t.Search.Allow(user) {
		return "", "", nil, skipped, t.SearchError(search.ErrRateLimited)
	}
	res, from, attempts, err := chain.TestEach(ctx, q)
	attempts = append(attempts, skipped...)
	if err != nil {
		return "", "", nil, attempts, t.SearchError(err)
	}
	if len(res) > q.MaxResults {
		res = res[:q.MaxResults]
	}
	return from.Name(), from.Host(), res, attempts, nil
}

func joinReasons(as []search.Attempt) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Provider+": "+a.Reason)
	}
	if len(out) == 0 {
		return "no provider is set"
	}
	return strings.Join(out, "; ")
}
