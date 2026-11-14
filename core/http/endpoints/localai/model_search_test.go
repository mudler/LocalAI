package localai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
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
		e.GET("/api/models/search", ModelSearchEndpoint(s, nil))
	})

	It("returns results and a status per provider, even when one fails", func() {
		rec := get("/api/models/search?q=qwen&limit=3&sort=likes&format=any")
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
		e.GET("/api/models/search", ModelSearchEndpoint(s, nil))

		rec := get("/api/models/search?sort=latest&limit=10")
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
