package search_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/search"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSearch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "search suite")
}

func fixture(name string) []byte {
	b, err := os.ReadFile("testdata/" + name)
	Expect(err).NotTo(HaveOccurred())
	return b
}

// pageGetter answers every Get with one page and records the URL.
type pageGetter struct {
	page []byte
	err  error
	got  []string
}

func (g *pageGetter) Get(_ context.Context, raw string, _ []string) (*safehttp.Response, error) {
	g.got = append(g.got, raw)
	if g.err != nil {
		return nil, g.err
	}
	return &safehttp.Response{ContentType: "text/html", Body: g.page}, nil
}

// countingProvider returns fixed results and counts the calls.
type countingProvider struct {
	mu    sync.Mutex
	calls int
}

func (*countingProvider) Name() string { return "fake" }
func (*countingProvider) Host() string { return "fake.example" }
func (p *countingProvider) Search(context.Context, search.Query) ([]search.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return []search.Result{{Title: "a", URL: "https://a.example/"}, {Title: "b", URL: "https://b.example/"}}, nil
}

var _ = Describe("DuckDuckGo", func() {
	It("reads results, drops ads, bad links and duplicates, and unwraps redirect links", func() {
		res, err := search.ParseDuckDuckGo(fixture("ddg-results.html"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal([]search.Result{
			{Title: "LocalAI documentation", URL: "https://localai.io/", Snippet: "The free, open source alternative to OpenAI."},
			{Title: "GitHub - mudler/LocalAI", URL: "https://github.com/mudler/LocalAI", Snippet: "Run models locally."},
		}))
	})

	It("reads an empty result page as no results", func() {
		res, err := search.ParseDuckDuckGo(fixture("ddg-noresults.html"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(BeEmpty())
	})

	It("reads a challenge page as a provider that does not work", func() {
		_, err := search.ParseDuckDuckGo(fixture("ddg-challenge.html"))
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
	})

	It("reads an unknown page as a provider that does not work", func() {
		_, err := search.ParseDuckDuckGo([]byte("<html><body><p>new layout</p></body></html>"))
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
	})

	It("sends the query with site and freshness", func() {
		g := &pageGetter{page: fixture("ddg-noresults.html")}
		p := search.NewDuckDuckGo(g, "https://ddg.test/html/")
		_, err := p.Search(context.Background(), search.Query{Text: "local ai", Site: "github.com", Freshness: "week", MaxResults: 5})
		Expect(err).NotTo(HaveOccurred())
		u, err := url.Parse(g.got[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(u.Query().Get("q")).To(Equal("local ai site:github.com"))
		Expect(u.Query().Get("df")).To(Equal("w"))
	})

	It("reports a fetch failure as a provider error and a timeout as a timeout", func() {
		p := search.NewDuckDuckGo(&pageGetter{err: safehttp.ErrBlockedAddress}, "https://ddg.test/html/")
		_, err := p.Search(context.Background(), search.Query{Text: "x", MaxResults: 5})
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		p = search.NewDuckDuckGo(&pageGetter{err: safehttp.ErrTimeout}, "https://ddg.test/html/")
		_, err = p.Search(context.Background(), search.Query{Text: "x", MaxResults: 5})
		Expect(err).To(MatchError(search.ErrTimeout))
	})
})

var _ = Describe("SearXNG", func() {
	It("reads results, keeps http(s) links once and fills a missing title", func() {
		res, err := search.ParseSearXNG(bytes.NewReader(fixture("searxng.json")))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal([]search.Result{
			{Title: "LocalAI", URL: "https://localai.io/", Snippet: "The free, open source alternative to OpenAI."},
			{Title: "GitHub - mudler/LocalAI", URL: "https://github.com/mudler/LocalAI", Snippet: "Run models locally."},
			{Title: "example.org", URL: "https://example.org/untitled", Snippet: ""},
		}))
	})

	It("queries the instance with the JSON format, freshness and site", func() {
		var got url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/searx/search"))
			got = r.URL.Query()
			_, _ = w.Write(fixture("searxng.json"))
		}))
		DeferCleanup(srv.Close)
		p, err := search.NewSearXNG(srv.URL + "/searx/")
		Expect(err).NotTo(HaveOccurred())
		res, err := p.Search(context.Background(), search.Query{Text: "localai", Freshness: "day", Site: "localai.io", MaxResults: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(3))
		Expect(got.Get("format")).To(Equal("json"))
		Expect(got.Get("time_range")).To(Equal("day"))
		Expect(got.Get("q")).To(Equal("localai site:localai.io"))
	})

	It("says what to change when the instance refuses JSON", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		DeferCleanup(srv.Close)
		p, err := search.NewSearXNG(srv.URL)
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Search(context.Background(), search.Query{Text: "x", MaxResults: 5})
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("search.formats"))
	})

	It("does not follow a redirect", func() {
		hit := false
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
		DeferCleanup(target.Close)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		DeferCleanup(srv.Close)
		p, err := search.NewSearXNG(srv.URL)
		Expect(err).NotTo(HaveOccurred())
		_, err = p.Search(context.Background(), search.Query{Text: "x", MaxResults: 5})
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		Expect(hit).To(BeFalse())
	})

	It("refuses a URL that is not http(s) or has credentials", func() {
		for _, u := range []string{"", "file:///x", "ftp://h/", "https://u:p@h/", "https://"} {
			_, err := search.NewSearXNG(u)
			Expect(err).To(HaveOccurred(), u)
		}
	})
})

var _ = Describe("Normalize", func() {
	DescribeTable("refuses",
		func(q search.Query) {
			_, err := search.Normalize(q)
			Expect(errors.Is(err, search.ErrBadQuery)).To(BeTrue())
		},
		Entry("empty", search.Query{Text: "  "}),
		Entry("too long", search.Query{Text: string(bytes.Repeat([]byte("a"), 401))}),
		Entry("too many results", search.Query{Text: "x", MaxResults: 11}),
		Entry("negative results", search.Query{Text: "x", MaxResults: -1}),
		Entry("unknown freshness", search.Query{Text: "x", Freshness: "hour"}),
		Entry("site with a path", search.Query{Text: "x", Site: "example.com/a"}),
		Entry("site with a space", search.Query{Text: "x", Site: "a b"}),
	)

	It("fills the default count and normalizes the site", func() {
		q, err := search.Normalize(search.Query{Text: " a   b ", Site: "Example.COM."})
		Expect(err).NotTo(HaveOccurred())
		Expect(q).To(Equal(search.Query{Text: "a b", MaxResults: 5, Site: "example.com"}))
	})
})

var _ = Describe("Searcher", func() {
	var (
		p   *countingProvider
		s   *search.Searcher
		now time.Time
	)

	BeforeEach(func() {
		p = &countingProvider{}
		now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		s = search.NewSearcher(p, search.Limits{PerUser: 3, Global: 5})
		s.SetClock(func() time.Time { return now })
	})

	It("answers an identical query from the cache for five minutes", func() {
		_, err := s.Search(context.Background(), "alice", search.Query{Text: "x"})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.Search(context.Background(), "bob", search.Query{Text: " X "})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.calls).To(Equal(1))
		now = now.Add(5 * time.Minute)
		_, err = s.Search(context.Background(), "alice", search.Query{Text: "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.calls).To(Equal(2))
	})

	It("keeps queries with other options apart", func() {
		_, _ = s.Search(context.Background(), "alice", search.Query{Text: "x"})
		_, _ = s.Search(context.Background(), "alice", search.Query{Text: "x", Site: "a.example"})
		_, _ = s.Search(context.Background(), "alice", search.Query{Text: "x", Freshness: "day"})
		Expect(p.calls).To(Equal(3))
	})

	It("limits each user per minute and does not count cache hits", func() {
		for i := range 3 {
			_, err := s.Search(context.Background(), "alice", search.Query{Text: string(rune('a' + i))})
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := s.Search(context.Background(), "alice", search.Query{Text: "a"})
		Expect(err).NotTo(HaveOccurred(), "a cache hit is free")
		_, err = s.Search(context.Background(), "alice", search.Query{Text: "new"})
		Expect(err).To(MatchError(search.ErrRateLimited))
		now = now.Add(time.Minute)
		_, err = s.Search(context.Background(), "alice", search.Query{Text: "new"})
		Expect(err).NotTo(HaveOccurred())
	})

	It("limits all users together", func() {
		users := []string{"a", "b", "c", "d", "e"}
		for i, u := range users {
			_, err := s.Search(context.Background(), u, search.Query{Text: u + string(rune('0'+i))})
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := s.Search(context.Background(), "f", search.Query{Text: "f"})
		Expect(err).To(MatchError(search.ErrRateLimited))
	})

	It("cuts the results at the count asked for", func() {
		res, err := s.Search(context.Background(), "alice", search.Query{Text: "x", MaxResults: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(1))
	})
})
