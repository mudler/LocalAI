// SPDX-License-Identifier: MIT
package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/web/internal/providers"
	"github.com/mudler/LocalAI/pkg/web/search"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProviders(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Web add-on providers suite")
}

// A stand-in key: the tests check that it reaches the fake and goes nowhere
// else.
const key = "test-key-0123456789"

type seen struct {
	method, path string
	query        map[string][]string
	header       http.Header
	body         map[string]any
}

// fake answers every request with status and body and records the request.
func fake(status int, body string) (*httptest.Server, *seen) {
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.method, s.path, s.query, s.header = r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone()
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &s.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	DeferCleanup(srv.Close)
	return srv, s
}

var q = search.Query{Text: "local ai", MaxResults: 3, Freshness: "week", Site: "github.com"}

var _ = Describe("Brave Search", func() {
	It("sends the key in its header, the query with site, count and freshness, and reads the results", func() {
		srv, got := fake(200, `{"web":{"results":[
			{"title":"<strong>LocalAI</strong> &amp; friends","url":"https://github.com/mudler/LocalAI","description":"Run <strong>models</strong> locally."},
			{"title":"dup","url":"https://github.com/mudler/LocalAI","description":""},
			{"title":"bad","url":"javascript:alert(1)","description":""},
			{"title":"","url":"https://example.org/x","description":"no title"}]}}`)
		p, err := providers.NewBrave(srv.URL+"/res/v1/web/search", key)
		Expect(err).NotTo(HaveOccurred())
		res, err := p.Search(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal([]search.Result{
			{Title: "LocalAI & friends", URL: "https://github.com/mudler/LocalAI", Snippet: "Run models locally."},
			{Title: "example.org", URL: "https://example.org/x", Snippet: "no title"},
		}))
		Expect(got.header.Get("X-Subscription-Token")).To(Equal(key))
		Expect(got.query["q"]).To(Equal([]string{"local ai site:github.com"}))
		Expect(got.query["count"]).To(Equal([]string{"3"}))
		Expect(got.query["freshness"]).To(Equal([]string{"pw"}))
		for k, v := range got.query {
			Expect(strings.Join(v, ",")).NotTo(ContainSubstring(key), k)
		}
		Expect(p.Name()).To(Equal("brave"))
		Expect(p.Host()).To(Equal("127.0.0.1"))
	})
})

var _ = Describe("Tavily", func() {
	It("posts the query with the key as a bearer token and reads the results", func() {
		srv, got := fake(200, `{"results":[{"title":"LocalAI","url":"https://localai.io/","content":"The free alternative."}]}`)
		p, err := providers.NewTavily(srv.URL+"/search", key)
		Expect(err).NotTo(HaveOccurred())
		res, err := p.Search(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal([]search.Result{{Title: "LocalAI", URL: "https://localai.io/", Snippet: "The free alternative."}}))
		Expect(got.method).To(Equal(http.MethodPost))
		Expect(got.header.Get("Authorization")).To(Equal("Bearer " + key))
		Expect(got.body).To(HaveKeyWithValue("query", "local ai"))
		Expect(got.body).To(HaveKeyWithValue("max_results", BeNumerically("==", 3)))
		Expect(got.body).To(HaveKeyWithValue("time_range", "week"))
		Expect(got.body).To(HaveKeyWithValue("include_domains", []any{"github.com"}))
		Expect(got.body).NotTo(HaveKey("api_key"))
	})
})

var _ = Describe("Google Programmable Search", func() {
	It("sends the key in its header, never in the URL, with the engine, site and freshness", func() {
		srv, got := fake(200, `{"items":[{"title":"LocalAI","link":"https://localai.io/","snippet":"Run models."}]}`)
		p, err := providers.NewGoogle(srv.URL+"/customsearch/v1", key, "engine-1:abc")
		Expect(err).NotTo(HaveOccurred())
		res, err := p.Search(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal([]search.Result{{Title: "LocalAI", URL: "https://localai.io/", Snippet: "Run models."}}))
		Expect(got.header.Get("X-Goog-Api-Key")).To(Equal(key))
		Expect(got.query).NotTo(HaveKey("key"))
		Expect(got.query["cx"]).To(Equal([]string{"engine-1:abc"}))
		Expect(got.query["num"]).To(Equal([]string{"3"}))
		Expect(got.query["dateRestrict"]).To(Equal([]string{"w1"}))
		Expect(got.query["siteSearch"]).To(Equal([]string{"github.com"}))
	})

	It("needs a search engine ID", func() {
		_, err := providers.NewGoogle(providers.GoogleEndpoint, key, "")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("the keyed providers", func() {
	build := map[string]func(endpoint string) (search.Provider, error){
		"brave":  func(e string) (search.Provider, error) { return providers.NewBrave(e, key) },
		"tavily": func(e string) (search.Provider, error) { return providers.NewTavily(e, key) },
		"google": func(e string) (search.Provider, error) { return providers.NewGoogle(e, key, "cx1") },
	}

	It("refuse a missing or malformed key", func() {
		for _, k := range []string{"", "short", "has space 12345", strings.Repeat("a", 257)} {
			_, err := providers.NewBrave(providers.BraveEndpoint, k)
			Expect(err).To(MatchError(providers.ErrNoKey), k)
			_, err = providers.NewTavily(providers.TavilyEndpoint, k)
			Expect(err).To(MatchError(providers.ErrNoKey), k)
			_, err = providers.NewGoogle(providers.GoogleEndpoint, k, "cx1")
			Expect(err).To(MatchError(providers.ErrNoKey), k)
		}
	})

	DescribeTable("say what went wrong without the key, the URL or the body",
		func(status int, want string) {
			for name, mk := range build {
				srv, _ := fake(status, `{"error":"bad key `+key+`"}`)
				p, err := mk(srv.URL + "/x")
				Expect(err).NotTo(HaveOccurred())
				_, err = p.Search(context.Background(), q)
				Expect(errors.Is(err, search.ErrProvider)).To(BeTrue(), name)
				Expect(err.Error()).To(ContainSubstring(want), name)
				Expect(err.Error()).NotTo(ContainSubstring(key), name)
				Expect(err.Error()).NotTo(ContainSubstring(srv.URL), name)
			}
		},
		Entry("a refused key", 401, "refused the key (401)"),
		Entry("a forbidden key", 403, "refused the key (403)"),
		Entry("a spent key", 429, "reached its limit (429)"),
		Entry("a server error", 500, "answered 500"),
	)

	It("read an answer that is not JSON as a provider that does not work", func() {
		for name, mk := range build {
			srv, _ := fake(200, `<html>`)
			p, _ := mk(srv.URL + "/x")
			_, err := p.Search(context.Background(), q)
			Expect(errors.Is(err, search.ErrProvider)).To(BeTrue(), name)
		}
	})

	It("follow no redirect, so the key header goes to no other host", func() {
		hit := false
		other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
		DeferCleanup(other.Close)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
		}))
		DeferCleanup(srv.Close)
		for name, mk := range build {
			p, _ := mk(srv.URL + "/x")
			_, err := p.Search(context.Background(), q)
			Expect(errors.Is(err, search.ErrProvider)).To(BeTrue(), name)
		}
		Expect(hit).To(BeFalse())
	})

	It("report an unreachable provider without its URL, and a slow one as a timeout", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		addr := srv.URL
		srv.Close()
		p, _ := providers.NewBrave(addr+"/x", key)
		_, err := p.Search(context.Background(), q)
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		Expect(err.Error()).NotTo(ContainSubstring(addr))

		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(300 * time.Millisecond):
			}
		}))
		DeferCleanup(slow.Close)
		p, _ = providers.NewTavily(slow.URL+"/x", key)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err = p.Search(ctx, q)
		Expect(err).To(MatchError(search.ErrTimeout))
	})

	It("list the keyed ones", func() {
		Expect(providers.Keyed("brave")).To(BeTrue())
		Expect(providers.Keyed("duckduckgo")).To(BeFalse())
		Expect(providers.ValidEngineID("a b")).To(BeFalse())
	})
})
