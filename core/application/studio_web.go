package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/search"
	"github.com/mudler/LocalAI/pkg/web/webtools"
	"github.com/mudler/xlog"
)

// webSettingsHint is where an administrator chooses the search provider. It
// is part of the error a user reads when no provider works.
const webSettingsHint = "an administrator can set a SearXNG instance in Settings > Tools, or switch web search off there"

// studioWeb serves the built-in web tools of the chats. It reads the runtime
// settings at each call and builds the fetch client and the searcher again
// when they change. The cache and the rate limits of the searcher are in the
// memory of this process: in a cluster each frontend has its own.
type studioWeb struct {
	cfg *config.ApplicationConfig

	mu    sync.Mutex
	key   string
	tools webtools.Tools
}

func newStudioWeb(cfg *config.ApplicationConfig) *studioWeb { return &studioWeb{cfg: cfg} }

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
	key := fmt.Sprint(searx, "\x00", w.cfg.ChatsWebAllowPrivate, "\x00", ports)
	w.mu.Lock()
	defer w.mu.Unlock()
	if key == w.key && w.tools.Fetch != nil {
		return w.tools
	}
	client := safehttp.New(safehttp.Policy{
		AllowPrivate: w.cfg.ChatsWebAllowPrivate,
		Ports:        ports,
		UserAgent:    "LocalAI/" + internal.PrintableVersion() + " (web_fetch; +https://localai.io)",
	}, nil)
	var provider search.Provider
	if searx != "" {
		p, err := search.NewSearXNG(searx)
		if err != nil {
			xlog.Warn("chats: the SearXNG URL is not valid; web search is unavailable", "error", err)
		} else {
			provider = p
		}
	} else {
		// DuckDuckGo is reached through the guarded client: its address is
		// fixed here, but the redirects it answers with are not.
		provider = search.NewDuckDuckGo(client, search.DuckDuckGoEndpoint)
	}
	t := webtools.Tools{Fetch: client, SettingsHint: webSettingsHint}
	if provider != nil {
		t.Search = search.NewSearcher(provider, search.Limits{})
	}
	w.key, w.tools = key, t
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

// TestSearch runs one search with the saved settings, for the Test search
// button of the settings. It returns the provider's name and host, the
// results, or the error a user would read.
func (w *studioWeb) TestSearch(ctx context.Context, user, query string) (provider, host string, results []search.Result, err error) {
	t := w.current()
	if t.Search == nil {
		return "", "", nil, errors.New("no search provider works: the SearXNG URL is not valid")
	}
	p := t.Search.Provider()
	res, err := t.Search.Search(ctx, user, search.Query{Text: query, MaxResults: 3})
	return p.Name(), p.Host(), res, err
}
