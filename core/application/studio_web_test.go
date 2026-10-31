package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/search"
	"github.com/mudler/LocalAI/pkg/web/webtools"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the chats' built-in web tools", func() {
	It("follows the runtime settings at each call", func() {
		cfg := &config.ApplicationConfig{}
		w := newStudioWeb(cfg)
		Expect(w.Enabled(webtools.SearchName)).To(BeTrue())
		Expect(w.Enabled(webtools.FetchName)).To(BeTrue())
		Expect(w.Enabled("bash")).To(BeFalse())

		cfg.DisableChatsWebFetch = true
		Expect(w.Enabled(webtools.FetchName)).To(BeFalse())
		_, err := w.Fetch(context.Background(), json.RawMessage(`{"url":"https://example.com/"}`))
		Expect(err).To(MatchError(ContainSubstring("switched off")))
	})

	It("searches with the keyless chain by default and with SearXNG alone when one is set", func() {
		cfg := &config.ApplicationConfig{}
		w := newStudioWeb(cfg)
		Expect(w.current().Search.Provider().Name()).To(Equal("duckduckgo+brave_html+bing_html"))
		cfg.ChatsWebSearXNGURL = "https://searx.example.org"
		Expect(w.current().Search.Provider().Name()).To(Equal("searxng"))
		Expect(w.current().Search.Provider().Host()).To(Equal("searx.example.org"))
	})

	It("follows the order of chats_web_search_providers, and leaves out SearXNG without an address", func() {
		cfg := &config.ApplicationConfig{ChatsWebSearchProviders: "bing_html, duckduckgo"}
		w := newStudioWeb(cfg)
		Expect(w.current().Search.Provider().Name()).To(Equal("bing_html+duckduckgo"))
		cfg.ChatsWebSearchProviders = "searxng,brave_html"
		Expect(w.current().Search.Provider().Name()).To(Equal("brave_html"))
		cfg.ChatsWebSearXNGURL = "https://searx.example.org"
		Expect(w.current().Search.Provider().Name()).To(Equal("searxng+brave_html"))
		cfg.ChatsWebSearchProviders = "altavista"
		Expect(w.current().Search.Provider().Name()).To(Equal("searxng"), "a bad list keeps the default")
	})

	Context("with the keyless pages on a test server", func() {
		var (
			cfg   *config.ApplicationConfig
			w     *studioWeb
			calls map[string]int
		)
		BeforeEach(func() {
			calls = map[string]int{}
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls[r.URL.Path]++
				mu.Unlock()
				rw.Header().Set("Content-Type", "text/html")
				switch r.URL.Path {
				case "/ddg":
					// The bot check of DuckDuckGo.
					rw.WriteHeader(http.StatusAccepted)
					_, _ = rw.Write([]byte(`<html><body><form id="challenge-form" action="/anomaly.js"></form></body></html>`))
				case "/brave":
					_, _ = rw.Write([]byte(`<html><body><div class="snippet" data-type="web"><a href="https://localai.io/"><div class="title">LocalAI</div></a><div class="generic-snippet"><div class="content">Run models.</div></div></div></body></html>`))
				default:
					rw.WriteHeader(http.StatusTooManyRequests)
				}
			}))
			DeferCleanup(srv.Close)
			u, _ := url.Parse(srv.URL)
			cfg = &config.ApplicationConfig{ChatsWebAllowPrivate: true, ChatsWebPorts: u.Port(), ChatsWebSearchProviders: "duckduckgo,bing_html,brave_html"}
			w = newStudioWeb(cfg)
			// The test server is on this machine, which web tools never reach.
			w.self = func() []netip.Prefix { return nil }
			w.endpoints = search.KeylessEndpoints{DuckDuckGo: srv.URL + "/ddg", Brave: srv.URL + "/brave", Bing: srv.URL + "/bing"}
		})

		It("answers from the first provider that works and labels the results with it", func() {
			out, err := w.Search(context.Background(), "alice", json.RawMessage(`{"query":"localai"}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(HavePrefix("Search results from search.brave.com"))
			Expect(out).To(ContainSubstring("https://localai.io/"))
			// The challenged and the blocked provider cool down: a new query
			// goes straight to Brave.
			_, err = w.Search(context.Background(), "alice", json.RawMessage(`{"query":"other"}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(calls["/ddg"]).To(Equal(1))
			Expect(calls["/bing"]).To(Equal(1))
			Expect(calls["/brave"]).To(Equal(2))
		})

		It("tests each provider and reports what each did", func() {
			p, host, res, attempts, err := w.TestSearch(context.Background(), "root", "localai")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).To(Equal("brave_html"))
			Expect(host).To(Equal("search.brave.com"))
			Expect(res).To(HaveLen(1))
			Expect(attempts).To(HaveLen(3))
			Expect([]search.Outcome{attempts[0].Outcome, attempts[1].Outcome, attempts[2].Outcome}).To(Equal([]search.Outcome{search.OutcomeChallenged, search.OutcomeBlocked, search.OutcomeOK}))
		})

		It("tells the user what each provider did and where to fix it when none works", func() {
			cfg.ChatsWebSearchProviders = "duckduckgo,bing_html"
			_, err := w.Search(context.Background(), "alice", json.RawMessage(`{"query":"localai"}`))
			Expect(err).To(MatchError("no search provider works. Tried duckduckgo: challenged by a bot check; bing_html: blocked this server (429 Too Many Requests). " +
				"An administrator can set a SearXNG instance in Settings > Tools, or install the Web add-on for search providers with a key from Add-ons."))
			_, _, _, attempts, err := w.TestSearch(context.Background(), "root", "localai")
			Expect(err).To(MatchError(ContainSubstring("no search provider works. Tried duckduckgo: challenged")))
			Expect(attempts).To(HaveLen(2))
		})

		It("reports SearXNG in the order without an address", func() {
			cfg.ChatsWebSearchProviders = "searxng"
			_, _, _, attempts, err := w.TestSearch(context.Background(), "root", "localai")
			Expect(err).To(MatchError(ContainSubstring("searxng: no SearXNG address is set")))
			Expect(attempts).To(Equal([]search.Attempt{{Provider: "searxng", Outcome: search.OutcomeError, Reason: "no SearXNG address is set"}}))
		})
	})

	It("refuses private addresses unless allowed, and keeps the ports of the setting", func() {
		cfg := &config.ApplicationConfig{}
		w := newStudioWeb(cfg)
		_, err := w.Fetch(context.Background(), json.RawMessage(`{"url":"http://127.0.0.1/"}`))
		Expect(err).To(MatchError(ContainSubstring("private network")))
		_, err = w.Fetch(context.Background(), json.RawMessage(`{"url":"http://example.com:8080/"}`))
		Expect(err).To(MatchError(ContainSubstring("port")))

		cfg.ChatsWebPorts = "80,443,8080"
		cfg.ChatsWebAllowPrivate = true
		c, ok := w.current().Fetch.(*safehttp.Client)
		Expect(ok).To(BeTrue())
		_, err = c.CheckURL("http://example.com:8080/")
		Expect(err).NotTo(HaveOccurred())
		_, err = c.CheckURL("http://10.0.0.1/")
		Expect(err).NotTo(HaveOccurred())
		_, err = c.CheckURL("http://169.254.169.254/")
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})
})
