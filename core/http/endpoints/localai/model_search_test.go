package localai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// latestStubProvider lists its newest models with no text, like Hugging Face.
type latestStubProvider struct{ stubSearchProvider }

func (*latestStubProvider) ListsLatest() bool { return true }

type stubSearchProvider struct {
	name string
	res  []modelsearch.Result
	err  error
	last modelsearch.Query
}

func (s *stubSearchProvider) Name() string { return s.name }
func (s *stubSearchProvider) Search(_ context.Context, q modelsearch.Query) ([]modelsearch.Result, error) {
	s.last = q
	return s.res, s.err
}

var _ = Describe("ModelSearchEndpoint", func() {
	var (
		hf, pf *stubSearchProvider
		e      *echo.Echo
	)

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	BeforeEach(func() {
		hf = &stubSearchProvider{name: "huggingface", res: []modelsearch.Result{{ID: "o/r", URI: "huggingface://o/r", Downloads: 7}}}
		pf = &stubSearchProvider{name: "pirateface", err: errors.New("down")}
		s := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: -1})
		s.Register(hf)
		s.Register(pf)
		e = echo.New()
		e.GET("/api/models/search", ModelSearchEndpoint(modelsearch.NewHost(s, nil, nil), nil))
	})

	It("returns results and a status per provider, even when one fails", func() {
		rec := get("/api/models/search?q=qwen&limit=3&sort=likes&format=any&providers=huggingface,pirateface")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var out modelsearch.Response
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Results).To(HaveLen(1))
		Expect(out.Results[0].Provider).To(Equal("huggingface"))
		Expect(out.Providers).To(Equal([]modelsearch.ProviderStatus{
			{Name: "huggingface", OK: true, Count: 1},
			{Name: "pirateface", Error: "down"},
		}))
		Expect(hf.last).To(Equal(modelsearch.Query{Text: "qwen", Limit: 3, Format: "any", Sort: "likes"}))
	})

	It("limits the providers asked", func() {
		rec := get("/api/models/search?q=x&providers=pirateface")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var out modelsearch.Response
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Providers).To(HaveLen(1))
		Expect(out.Providers[0].Name).To(Equal("pirateface"))
		Expect(hf.last.Text).To(BeEmpty())
	})

	It("answers an empty list as [] not null", func() {
		hf.res = nil
		rec := get("/api/models/search?q=x&providers=huggingface")
		Expect(rec.Body.String()).To(ContainSubstring(`"results":[]`))
	})

	It("takes an empty q with sort=latest, asks only a provider that lists its newest, and says the other cannot", func() {
		l := &latestStubProvider{stubSearchProvider{name: "huggingface", res: []modelsearch.Result{{ID: "o/new", URI: "huggingface://o/new"}}}}
		s := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: -1})
		s.Register(l)
		s.Register(pf)
		e = echo.New()
		e.GET("/api/models/search", ModelSearchEndpoint(modelsearch.NewHost(s, nil, nil), nil))

		rec := get("/api/models/search?sort=latest&limit=10&providers=huggingface,pirateface")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var out modelsearch.Response
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Results).To(HaveLen(1))
		Expect(out.Providers).To(Equal([]modelsearch.ProviderStatus{
			{Name: "huggingface", OK: true, Count: 1},
			{Name: "pirateface", OK: true, Unsupported: true},
		}))
		Expect(l.last).To(Equal(modelsearch.Query{Limit: 10, Format: "gguf", Sort: "latest"}))
		Expect(pf.last).To(Equal(modelsearch.Query{}))
	})

	DescribeTable("rejects a bad request",
		func(target, want string) {
			rec := get(target)
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(rec.Body.String()).To(ContainSubstring(want))
		},
		Entry("no q", "/api/models/search", "q is required"),
		Entry("blank q", "/api/models/search?q=%20", "q is required"),
		Entry("no q with another sort", "/api/models/search?sort=likes", "unless sort is latest"),
		Entry("bad limit", "/api/models/search?q=x&limit=abc", "limit"),
		Entry("unknown provider", "/api/models/search?q=x&providers=nope", "unknown provider"),
	)
})

// stubBroker is one add-on that provides a source with a filter.
type stubBroker struct {
	installed bool
	last      json.RawMessage
}

func (b *stubBroker) Sources(context.Context, string) ([]modelsearch.AddonSource, error) {
	if !b.installed {
		return nil, nil
	}
	return []modelsearch.AddonSource{{AddonID: "peer-search", Tool: "search", Running: true, Search: contract.ModelSearch{
		Source: contract.ModelSearchSource{ID: "peer", Title: "Peer"},
		Filters: []contract.SearchFilter{
			{ID: "kind", Type: contract.FilterEnum, Label: "Kind", Values: []string{"a", "b"}, Default: "a"},
			{ID: "seeds", Type: contract.FilterRange, Label: "Seeds", Min: 0, Max: 10},
		}}}}, nil
}

func (b *stubBroker) Call(_ context.Context, _, _, _ string, args json.RawMessage) (json.RawMessage, error) {
	b.last = args
	return json.RawMessage(`{"results":[{"id":"o/peer","uri":"pirateface://o/peer"},{"id":"o/bad","uri":"ftp://x"}]}`), nil
}

type mapKV struct{ m map[string]string }

func (k *mapKV) Get(_ context.Context, u string) (string, bool, error) {
	v, ok := k.m[u]
	return v, ok, nil
}
func (k *mapKV) Set(_ context.Context, u, v string) error { k.m[u] = v; return nil }

var _ = Describe("ModelSearchEndpoint with add-on sources", func() {
	var (
		e      *echo.Echo
		broker *stubBroker
		user   string
	)
	do := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, r)
		req.Header.Set("Content-Type", "application/json")
		e.ServeHTTP(rec, req)
		return rec
	}
	BeforeEach(func() {
		broker = &stubBroker{installed: true}
		s := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: -1})
		s.Register(&stubSearchProvider{name: "huggingface", res: []modelsearch.Result{{ID: "o/hf", URI: "huggingface://o/hf"}}})
		h := modelsearch.NewHost(s, broker, &mapKV{m: map[string]string{}})
		e = echo.New()
		user = "alice"
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("auth_user", &auth.User{ID: user})
				return next(c)
			}
		})
		e.GET("/api/models/search", ModelSearchEndpoint(h, nil))
		e.GET("/api/models/search/sources", ModelSearchSourcesEndpoint(h))
		e.PUT("/api/models/search/sources", ModelSearchSetSourcesEndpoint(h))
	})

	decode := func(rec *httptest.ResponseRecorder) modelsearch.Response {
		var out modelsearch.Response
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		return out
	}

	It("searches Hugging Face and every installed add-on source by default", func() {
		out := decode(do(http.MethodGet, "/api/models/search?q=x", ""))
		Expect(out.Providers).To(HaveLen(2))
		Expect(out.Sources).To(HaveLen(2))
		var ov modelsearch.Overview
		Expect(json.Unmarshal(do(http.MethodGet, "/api/models/search/sources", "").Body.Bytes(), &ov)).To(Succeed())
		Expect(ov.Enabled).To(Equal([]string{"huggingface", "peer"}))
		Expect(ov.Missing).To(BeEmpty())
	})

	It("searches the enabled set, passes filters to their source and counts dropped rows", func() {
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":["huggingface","peer"]}`).Code).To(Equal(http.StatusOK))
		rec := do(http.MethodGet, "/api/models/search?q=x&filters.peer.kind=b&filters.peer.seeds=3", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		out := decode(rec)
		Expect(out.Providers).To(HaveLen(2))
		Expect(out.Providers[1]).To(Equal(modelsearch.ProviderStatus{Name: "peer", Title: "Peer", OK: true, Count: 1, Dropped: 1}))
		Expect(out.Sources[1].Filters).To(HaveLen(2))
		Expect(string(broker.last)).To(ContainSubstring(`"filters":{"kind":"b","seeds":3}`))
	})

	It("sends the declared default of a filter the request leaves out", func() {
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":["peer"]}`).Code).To(Equal(http.StatusOK))
		Expect(do(http.MethodGet, "/api/models/search?q=x", "").Code).To(Equal(http.StatusOK))
		Expect(string(broker.last)).To(ContainSubstring(`"filters":{"kind":"a"}`))
		Expect(do(http.MethodGet, "/api/models/search?q=y&filters.peer.kind=b", "").Code).To(Equal(http.StatusOK))
		Expect(string(broker.last)).To(ContainSubstring(`"filters":{"kind":"b"}`))
	})

	DescribeTable("rejects a filter that does not fit, naming it",
		func(query, want string) {
			Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":["huggingface","peer"]}`).Code).To(Equal(http.StatusOK))
			rec := do(http.MethodGet, "/api/models/search?q=x&"+query, "")
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(rec.Body.String()).To(ContainSubstring(want))
		},
		Entry("unknown filter", "filters.peer.nope=1", `filter \"nope\"`),
		Entry("bad enum value", "filters.peer.kind=z", `filter \"kind\"`),
		Entry("range out of bounds", "filters.peer.seeds=99", `filter \"seeds\"`),
		Entry("range that is not a number", "filters.peer.seeds=many", `filter \"seeds\"`),
		Entry("unknown source", "filters.ghost.kind=a", "unknown provider"),
		Entry("malformed key", "filters.peer=a", "filter parameter"),
		Entry("filter on the built-in source", "filters.huggingface.kind=a", `filter \"kind\"`),
	)

	It("answers an empty set with an empty result and the hint data in the sources list", func() {
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":[]}`).Code).To(Equal(http.StatusOK))
		out := decode(do(http.MethodGet, "/api/models/search?q=x", ""))
		Expect(out.Results).To(BeEmpty())
		Expect(out.Providers).To(BeEmpty())
	})

	It("keeps the choice of one person apart from another", func() {
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":["huggingface"]}`).Code).To(Equal(http.StatusOK))
		Expect(decode(do(http.MethodGet, "/api/models/search?q=x", "")).Providers).To(HaveLen(1))
		user = "bob"
		out := decode(do(http.MethodGet, "/api/models/search?q=x", ""))
		Expect(out.Providers).To(HaveLen(2))
	})

	It("refuses a source that is not installed, and keeps a source turned off across a reinstall", func() {
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":["ghost"]}`).Code).To(Equal(http.StatusBadRequest))
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":["huggingface"]}`).Code).To(Equal(http.StatusOK))
		broker.installed = false
		var ov modelsearch.Overview
		Expect(json.Unmarshal(do(http.MethodGet, "/api/models/search/sources", "").Body.Bytes(), &ov)).To(Succeed())
		Expect(ov.Missing).To(BeEmpty())
		Expect(ov.Sources).To(HaveLen(1))
		Expect(do(http.MethodGet, "/api/models/search?q=x&providers=peer", "").Code).To(Equal(http.StatusBadRequest))
		broker.installed = true
		Expect(json.Unmarshal(do(http.MethodGet, "/api/models/search/sources", "").Body.Bytes(), &ov)).To(Succeed())
		Expect(ov.Sources).To(HaveLen(2))
		Expect(ov.Enabled).To(Equal([]string{"huggingface"}))
		Expect(decode(do(http.MethodGet, "/api/models/search?q=x", "")).Providers).To(HaveLen(1))
	})

	It("answers 400 for a list longer than the sources that can be in it", func() {
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":["huggingface","huggingface","huggingface","huggingface"]}`).Code).To(Equal(http.StatusBadRequest))
		var many []string
		for range 70 {
			many = append(many, `"huggingface"`)
		}
		Expect(do(http.MethodPut, "/api/models/search/sources", `{"enabled":[`+strings.Join(many, ",")+`]}`).Code).To(Equal(http.StatusBadRequest))
	})

	It("needs the body to carry the list", func() {
		Expect(do(http.MethodPut, "/api/models/search/sources", `{}`).Code).To(Equal(http.StatusBadRequest))
	})
})
