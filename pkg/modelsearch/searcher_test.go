package modelsearch_test

import (
	stdctx "context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func context() stdctx.Context { return stdctx.Background() }

type fakeProvider struct {
	name  string
	res   []modelsearch.Result
	err   error
	block bool
	calls atomic.Int32
	last  modelsearch.Query
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Search(ctx stdctx.Context, q modelsearch.Query) ([]modelsearch.Result, error) {
	f.calls.Add(1)
	f.last = q
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.res, f.err
}

// latestProvider can list its newest models with no text, like Hugging Face.
type latestProvider struct{ fakeProvider }

func (l *latestProvider) ListsLatest() bool { return true }

var _ = Describe("Searcher", func() {
	var a, b *fakeProvider
	var s *modelsearch.Searcher

	BeforeEach(func() {
		a = &fakeProvider{name: "a", res: []modelsearch.Result{{ID: "o/a1"}, {ID: "o/a2"}}}
		b = &fakeProvider{name: "b", res: []modelsearch.Result{{ID: "o/b1"}}}
		s = modelsearch.NewSearcher(modelsearch.Options{Timeout: 100 * time.Millisecond})
		s.Register(a)
		s.Register(b)
	})

	It("queries every provider by default and tags the results", func() {
		r := s.Search(context(), modelsearch.Query{Text: "x"}, nil)
		Expect(r.Results).To(HaveLen(3))
		Expect(r.Results[0].Provider).To(Equal("a"))
		Expect(r.Results[2].Provider).To(Equal("b"))
		Expect(r.Providers).To(Equal([]modelsearch.ProviderStatus{
			{Name: "a", OK: true, Count: 2}, {Name: "b", OK: true, Count: 1},
		}))
	})

	It("returns partial results when a provider fails or times out", func() {
		a.err = errors.New("boom")
		b.block = true
		c := &fakeProvider{name: "c", res: []modelsearch.Result{{ID: "o/c1"}}}
		s.Register(c)
		r := s.Search(context(), modelsearch.Query{Text: "x"}, nil)
		Expect(r.Results).To(HaveLen(1))
		Expect(r.Providers[0].OK).To(BeFalse())
		Expect(r.Providers[0].Error).To(Equal("boom"))
		Expect(r.Providers[1].OK).To(BeFalse())
		Expect(r.Providers[1].Error).To(ContainSubstring("deadline"))
		Expect(r.Providers[2].OK).To(BeTrue())
	})

	It("selects providers and reports unknown ones", func() {
		r := s.Search(context(), modelsearch.Query{Text: "x"}, modelsearch.ParseNames("b, nope,B"))
		Expect(r.Results).To(HaveLen(1))
		Expect(r.Providers).To(HaveLen(2))
		Expect(r.Providers[1]).To(Equal(modelsearch.ProviderStatus{Name: "nope", Error: "unknown provider"}))
		Expect(a.calls.Load()).To(BeZero())
	})

	It("normalizes the query and caps the limit", func() {
		s.Search(context(), modelsearch.Query{Text: "  x ", Limit: 999, Sort: "weird"}, []string{"a"})
		Expect(a.last).To(Equal(modelsearch.Query{Text: "x", Limit: modelsearch.MaxLimit, Format: "gguf", Sort: "downloads"}))
		r := s.Search(context(), modelsearch.Query{Text: "y", Limit: 1}, []string{"a"})
		Expect(r.Results).To(HaveLen(1))
	})

	It("caches successes but not failures", func() {
		s.Search(context(), modelsearch.Query{Text: "Same"}, []string{"a"})
		r := s.Search(context(), modelsearch.Query{Text: "same"}, []string{"a"})
		Expect(a.calls.Load()).To(Equal(int32(1)))
		Expect(r.Providers[0].Cached).To(BeTrue())

		b.err = errors.New("down")
		s.Search(context(), modelsearch.Query{Text: "z"}, []string{"b"})
		s.Search(context(), modelsearch.Query{Text: "z"}, []string{"b"})
		Expect(b.calls.Load()).To(Equal(int32(2)))
	})

	It("expires cache entries and can disable the cache", func() {
		off := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: -1})
		off.Register(a)
		off.Search(context(), modelsearch.Query{Text: "q"}, nil)
		off.Search(context(), modelsearch.Query{Text: "q"}, nil)
		Expect(a.calls.Load()).To(Equal(int32(2)))

		short := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: time.Millisecond})
		short.Register(b)
		short.Search(context(), modelsearch.Query{Text: "q"}, nil)
		time.Sleep(5 * time.Millisecond)
		short.Search(context(), modelsearch.Query{Text: "q"}, nil)
		Expect(b.calls.Load()).To(Equal(int32(2)))
	})

	It("keeps the cache bounded", func() {
		small := modelsearch.NewSearcher(modelsearch.Options{CacheSize: 2})
		small.Register(a)
		for _, q := range []string{"1", "2", "3", "1"} {
			small.Search(context(), modelsearch.Query{Text: q}, nil)
		}
		Expect(a.calls.Load()).To(BeNumerically(">=", 3))
	})

	Describe("the newest models with no search text", func() {
		It("asks only a provider that lists its newest, and says the other one cannot", func() {
			l := &latestProvider{fakeProvider{name: "l", res: []modelsearch.Result{{ID: "o/new1"}}}}
			s.Register(l)
			r := s.Search(context(), modelsearch.Query{Sort: modelsearch.SortLatest}, nil)
			Expect(l.calls.Load()).To(Equal(int32(1)))
			Expect(l.last.Text).To(BeEmpty())
			Expect(l.last.Sort).To(Equal(modelsearch.SortLatest))
			// a and b do not implement LatestLister: not called, not failed.
			Expect(a.calls.Load()).To(BeZero())
			Expect(b.calls.Load()).To(BeZero())
			Expect(r.Results).To(HaveLen(1))
			Expect(r.Providers).To(Equal([]modelsearch.ProviderStatus{
				{Name: "a", OK: true, Unsupported: true},
				{Name: "b", OK: true, Unsupported: true},
				{Name: "l", OK: true, Count: 1},
			}))
		})

		It("does not call anyone for an empty text with another sort", func() {
			l := &latestProvider{fakeProvider{name: "l"}}
			s.Register(l)
			r := s.Search(context(), modelsearch.Query{Sort: modelsearch.SortLikes}, []string{"l"})
			Expect(l.calls.Load()).To(BeZero())
			Expect(r.Providers[0].Unsupported).To(BeTrue())
		})

		It("caches the list like a search", func() {
			l := &latestProvider{fakeProvider{name: "l", res: []modelsearch.Result{{ID: "o/new1"}}}}
			s.Register(l)
			q := modelsearch.Query{Sort: modelsearch.SortLatest, Limit: 10}
			s.Search(context(), q, []string{"l"})
			r := s.Search(context(), q, []string{"l"})
			Expect(l.calls.Load()).To(Equal(int32(1)))
			Expect(r.Providers[0].Cached).To(BeTrue())
			// Another page size is another answer.
			s.Search(context(), modelsearch.Query{Sort: modelsearch.SortLatest, Limit: 20}, []string{"l"})
			Expect(l.calls.Load()).To(Equal(int32(2)))
		})

		It("keeps latest as a sort", func() {
			Expect(modelsearch.Query{Sort: " Latest "}.Normalize().Sort).To(Equal(modelsearch.SortLatest))
		})
	})
})
