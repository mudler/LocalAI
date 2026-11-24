package search_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/search"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Brave Search page", func() {
	It("reads the web results, drops other blocks, Brave's own links, bad links and duplicates", func() {
		res, err := search.ParseBrave(fixture("brave-results.html"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal([]search.Result{
			{Title: "LocalAI documentation", URL: "https://localai.io/", Snippet: "October 1, 2026 - The free, open source alternative to OpenAI."},
			{Title: "GitHub - mudler/LocalAI", URL: "https://github.com/mudler/LocalAI", Snippet: "Run models locally."},
			{Title: "Example page", URL: "https://example.org/no-title", Snippet: ""},
		}))
	})

	It("reads a bot check as challenged", func() {
		_, err := search.ParseBrave(fixture("brave-challenge.html"))
		Expect(errors.Is(err, search.ErrChallenged)).To(BeTrue())
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
	})

	It("reads an unknown page as a provider that does not work, not as challenged", func() {
		_, err := search.ParseBrave([]byte("<html><body><p>new layout</p></body></html>"))
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		Expect(errors.Is(err, search.ErrChallenged)).To(BeFalse())
	})

	It("sends the query with site and freshness", func() {
		g := &pageGetter{page: fixture("brave-results.html")}
		_, err := search.NewBrave(g, "https://brave.test/search").Search(context.Background(), search.Query{Text: "local ai", Site: "github.com", Freshness: "month", MaxResults: 5})
		Expect(err).NotTo(HaveOccurred())
		u, err := url.Parse(g.got[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(u.Query().Get("q")).To(Equal("local ai site:github.com"))
		Expect(u.Query().Get("tf")).To(Equal("pm"))
		Expect(u.Query().Get("source")).To(Equal("web"))
	})
})

var _ = Describe("Bing page", func() {
	It("reads the organic results, drops ads, Bing's own links, bad links and duplicates, and unwraps /ck/a links", func() {
		res, err := search.ParseBing(fixture("bing-results.html"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal([]search.Result{
			{Title: "LocalAI documentation", URL: "https://localai.io/", Snippet: "1 Oct 2026 · The free, open source alternative to OpenAI."},
			{Title: "GitHub - mudler/LocalAI", URL: "https://github.com/mudler/LocalAI", Snippet: "Run models locally."},
		}))
	})

	It("reads an empty result page as no results", func() {
		res, err := search.ParseBing(fixture("bing-noresults.html"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(BeEmpty())
	})

	It("reads a bot check as challenged", func() {
		_, err := search.ParseBing(fixture("bing-challenge.html"))
		Expect(errors.Is(err, search.ErrChallenged)).To(BeTrue())
	})

	It("reads an unknown page as a provider that does not work", func() {
		_, err := search.ParseBing([]byte("<html><body><p>new layout</p></body></html>"))
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		Expect(errors.Is(err, search.ErrChallenged)).To(BeFalse())
	})

	It("sends the query with site and freshness", func() {
		g := &pageGetter{page: fixture("bing-noresults.html")}
		p := search.NewBing(g, "https://bing.test/search")
		_, err := p.Search(context.Background(), search.Query{Text: "local ai", Site: "github.com", Freshness: "week", MaxResults: 5})
		Expect(err).NotTo(HaveOccurred())
		u, err := url.Parse(g.got[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(u.Query().Get("q")).To(Equal("local ai site:github.com"))
		Expect(u.Query().Get("filters")).To(Equal(`ex1:"ez2"`))
		_, err = p.Search(context.Background(), search.Query{Text: "x", Freshness: "year", MaxResults: 5})
		Expect(err).NotTo(HaveOccurred())
		u, _ = url.Parse(g.got[1])
		Expect(u.Query().Get("filters")).To(MatchRegexp(`^ex1:"ez5_\d+_\d+"$`))
	})
})

var _ = Describe("DuckDuckGo bot check", func() {
	It("reads the anomaly page as challenged", func() {
		_, err := search.ParseDuckDuckGo(fixture("ddg-anomaly.html"))
		Expect(errors.Is(err, search.ErrChallenged)).To(BeTrue())
		_, err = search.ParseDuckDuckGo(fixture("ddg-challenge.html"))
		Expect(errors.Is(err, search.ErrChallenged)).To(BeTrue())
	})

	It("reads an unreadable answer with status 202 as challenged", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("<html><body><p>please wait</p></body></html>"))
		}))
		defer srv.Close()
		_, err := search.NewDuckDuckGo(localClient(srv), srv.URL+"/html/").Search(context.Background(), search.Query{Text: "x", MaxResults: 5})
		Expect(errors.Is(err, search.ErrChallenged)).To(BeTrue())
		Expect(search.OutcomeOf(nil, err)).To(Equal(search.OutcomeChallenged))
	})
})

// localClient is a guarded client that may reach the test server.
func localClient(srv *httptest.Server) *safehttp.Client {
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return safehttp.New(safehttp.Policy{AllowPrivate: true, Ports: []int{port}, Timeout: 5 * time.Second}, nil)
}

var _ = Describe("Keyless chain", func() {
	var (
		srv   *httptest.Server
		mu    sync.Mutex
		calls map[string]int
		// pages maps a path to its status and fixture.
		pages map[string]struct {
			status int
			file   string
		}
		chain *search.Fallback
		clock time.Time
	)

	set := func(path string, status int, file string) {
		mu.Lock()
		defer mu.Unlock()
		pages[path] = struct {
			status int
			file   string
		}{status, file}
	}

	BeforeEach(func() {
		calls = map[string]int{}
		pages = map[string]struct {
			status int
			file   string
		}{}
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls[r.URL.Path]++
			p := pages[r.URL.Path]
			mu.Unlock()
			if p.file == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(p.status)
			_, _ = w.Write(fixture(p.file))
		}))
		DeferCleanup(srv.Close)
		c := localClient(srv)
		e := search.KeylessEndpoints{DuckDuckGo: srv.URL + "/ddg", Brave: srv.URL + "/brave", Bing: srv.URL + "/bing"}
		var ps []search.Provider
		for _, n := range search.KeylessOrder {
			p, err := search.NewKeyless(n, c, e)
			Expect(err).NotTo(HaveOccurred())
			ps = append(ps, p)
		}
		clock = time.Unix(1_800_000_000, 0)
		chain = search.NewFallback(0, ps...).WithCooldown(time.Minute)
		chain.SetClock(func() time.Time { return clock })
	})

	q := search.Query{Text: "localai", MaxResults: 5}

	It("falls back from a challenged DuckDuckGo to Brave and labels the results with Brave", func() {
		set("/ddg", http.StatusAccepted, "ddg-anomaly.html")
		set("/brave", http.StatusOK, "brave-results.html")
		set("/bing", http.StatusOK, "bing-results.html")
		res, from, attempts, err := chain.SearchAttempts(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(3))
		Expect(from.Host()).To(Equal("search.brave.com"))
		Expect(attempts).To(HaveLen(2))
		Expect(attempts[0].Outcome).To(Equal(search.OutcomeChallenged))
		Expect(attempts[1]).To(Equal(search.Attempt{Provider: "brave_html", Host: "search.brave.com", Outcome: search.OutcomeOK, Results: 3}))
		Expect(calls["/bing"]).To(Equal(0))
	})

	It("skips a challenged provider during its cool-down and asks it again after", func() {
		set("/ddg", http.StatusAccepted, "ddg-anomaly.html")
		set("/brave", http.StatusOK, "brave-results.html")
		_, _, _, err := chain.SearchAttempts(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		_, _, attempts, err := chain.SearchAttempts(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(calls["/ddg"]).To(Equal(1), "DuckDuckGo is cooling down")
		Expect(attempts[0].Outcome).To(Equal(search.OutcomeSkipped))

		set("/ddg", http.StatusOK, "ddg-results.html")
		clock = clock.Add(2 * time.Minute)
		_, from, _, err := chain.SearchAttempts(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(from.Name()).To(Equal("duckduckgo"))
		Expect(calls["/ddg"]).To(Equal(2))
	})

	It("cools down a provider that blocks this server", func() {
		set("/ddg", http.StatusTooManyRequests, "ddg-noresults.html")
		set("/brave", http.StatusOK, "brave-results.html")
		_, _, attempts, err := chain.SearchAttempts(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(attempts[0].Outcome).To(Equal(search.OutcomeBlocked))
		Expect(attempts[0].Reason).To(ContainSubstring("429"))
		_, _, attempts, _ = chain.SearchAttempts(context.Background(), q)
		Expect(attempts[0].Outcome).To(Equal(search.OutcomeSkipped))
		Expect(attempts[0].Reason).To(ContainSubstring("blocked this server"))
	})

	It("asks every provider when all are cooling down, and says what each did", func() {
		set("/ddg", http.StatusAccepted, "ddg-anomaly.html")
		set("/brave", http.StatusOK, "brave-challenge.html")
		set("/bing", http.StatusOK, "bing-challenge.html")
		_, _, _, err := chain.SearchAttempts(context.Background(), q)
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		Expect(err.Error()).To(Equal("the search provider did not answer with results: duckduckgo: challenged by a bot check; brave_html: challenged by a bot check; bing_html: challenged by a bot check"))
		_, _, _, err = chain.SearchAttempts(context.Background(), q)
		Expect(err).To(HaveOccurred())
		Expect(calls["/ddg"]).To(Equal(2))
		Expect(calls["/bing"]).To(Equal(2))
	})

	It("stops at an empty answer, which is an answer", func() {
		set("/ddg", http.StatusOK, "ddg-noresults.html")
		res, from, _, err := chain.SearchAttempts(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(BeEmpty())
		Expect(from.Name()).To(Equal("duckduckgo"))
		Expect(calls["/brave"]).To(Equal(0))
	})

	It("tests every provider and clears the cool-down of one that answers", func() {
		set("/ddg", http.StatusAccepted, "ddg-anomaly.html")
		// A page that is not results: the layout changed.
		set("/brave", http.StatusOK, "searxng.json")
		set("/bing", http.StatusOK, "bing-noresults.html")
		res, from, got, err := chain.TestEach(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(BeEmpty())
		Expect(from.Name()).To(Equal("bing_html"), "the first answer is Bing's empty one")
		Expect(got).To(HaveLen(3))
		Expect([]search.Outcome{got[0].Outcome, got[1].Outcome, got[2].Outcome}).To(Equal([]search.Outcome{search.OutcomeChallenged, search.OutcomeError, search.OutcomeNoResults}))
		Expect(got[1].Reason).To(Equal("the page had no results it could read"))

		set("/ddg", http.StatusOK, "ddg-results.html")
		res, from, got, err = chain.TestEach(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].Outcome).To(Equal(search.OutcomeOK))
		Expect(from.Name()).To(Equal("duckduckgo"))
		Expect(res).To(HaveLen(2))
		_, _, _, err = search.NewFallback(0).TestEach(context.Background(), q)
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		_, from, _, err = chain.SearchAttempts(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(from.Name()).To(Equal("duckduckgo"), "the test cleared the cool-down")
	})

	It("refuses an unknown keyless name", func() {
		_, err := search.NewKeyless("altavista", nil, search.DefaultKeylessEndpoints())
		Expect(err).To(HaveOccurred())
		Expect(search.IsKeyless("brave_html")).To(BeTrue())
		Expect(search.IsKeyless("searxng")).To(BeFalse())
	})
})

var _ = Describe("Outcomes", func() {
	DescribeTable("classify an answer",
		func(res []search.Result, err error, want search.Outcome) {
			Expect(search.OutcomeOf(res, err)).To(Equal(want))
		},
		Entry("results", []search.Result{{Title: "a", URL: "https://a.example/"}}, nil, search.OutcomeOK),
		Entry("no results", nil, nil, search.OutcomeNoResults),
		Entry("a bot check", nil, fmt.Errorf("%w: %w", search.ErrProvider, search.ErrChallenged), search.OutcomeChallenged),
		Entry("a refusal", nil, fmt.Errorf("%w: %w", search.ErrProvider, search.ErrBlocked), search.OutcomeBlocked),
		Entry("a timeout", nil, search.ErrTimeout, search.OutcomeTimeout),
		Entry("another failure", nil, search.ErrProvider, search.OutcomeError),
	)
})
