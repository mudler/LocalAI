package search_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/pkg/web/search"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// scripted answers with fixed results or a fixed error, or waits for the end
// of its context when hang is set.
type scripted struct {
	name, host string
	results    []search.Result
	err        error
	hang       bool
	calls      int
}

func (p *scripted) Name() string { return p.name }
func (p *scripted) Host() string { return p.host }
func (p *scripted) Search(ctx context.Context, _ search.Query) ([]search.Result, error) {
	p.calls++
	if p.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.results, p.err
}

var _ = Describe("Fallback", func() {
	q := search.Query{Text: "x", MaxResults: 5}

	It("returns the first answer and the provider that gave it", func() {
		a := &scripted{name: "a", host: "a.example", err: fmt.Errorf("%w: the key was refused", search.ErrProvider)}
		b := &scripted{name: "b", host: "b.example", results: []search.Result{{Title: "t", URL: "https://b.example/"}}}
		c := &scripted{name: "c", host: "c.example"}
		f := search.NewFallback(0, a, b, c)
		res, from, err := f.SearchFrom(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(1))
		Expect(from.Host()).To(Equal("b.example"))
		Expect(c.calls).To(Equal(0), "an answer stops the chain")
		Expect(f.Name()).To(Equal("a+b+c"))
		Expect(f.Host()).To(Equal("a.example"))
	})

	It("takes an answer with no results as an answer", func() {
		a := &scripted{name: "a", host: "a.example"}
		b := &scripted{name: "b", host: "b.example", results: []search.Result{{Title: "t", URL: "https://b.example/"}}}
		res, from, err := search.NewFallback(0, a, b).SearchFrom(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(BeEmpty())
		Expect(from.Name()).To(Equal("a"))
		Expect(b.calls).To(Equal(0))
	})

	It("moves on from a provider that does not answer in time", func() {
		a := &scripted{name: "a", host: "a.example", hang: true}
		b := &scripted{name: "b", host: "b.example", results: []search.Result{{Title: "t", URL: "https://b.example/"}}}
		_, from, err := search.NewFallback(20*time.Millisecond, a, b).SearchFrom(context.Background(), q)
		Expect(err).NotTo(HaveOccurred())
		Expect(from.Name()).To(Equal("b"))
	})

	It("names every provider and its reason when none answers", func() {
		a := &scripted{name: "a", host: "a.example", err: fmt.Errorf("%w: the key was refused", search.ErrProvider)}
		b := &scripted{name: "b", host: "b.example", err: search.ErrTimeout}
		_, _, err := search.NewFallback(0, a, b).SearchFrom(context.Background(), q)
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
		Expect(err.Error()).To(Equal("the search provider did not answer with results: a: the key was refused; b: it did not answer in time"))
	})

	It("stops when the caller stops", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b := &scripted{name: "b", host: "b.example"}
		_, _, err := search.NewFallback(0, b).SearchFrom(ctx, q)
		Expect(err).To(MatchError(context.Canceled))
		Expect(b.calls).To(Equal(0))
	})

	It("refuses to search with no provider", func() {
		_, err := search.NewFallback(0).Search(context.Background(), q)
		Expect(errors.Is(err, search.ErrProvider)).To(BeTrue())
	})

	It("labels the results of a Searcher with the host that answered, also from the cache", func() {
		a := &scripted{name: "a", host: "a.example", err: search.ErrProvider}
		b := &scripted{name: "b", host: "b.example", results: []search.Result{{Title: "t", URL: "https://b.example/"}}}
		s := search.NewSearcher(search.NewFallback(0, a, b), search.Limits{})
		_, host, err := s.SearchHost(context.Background(), "alice", q)
		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(Equal("b.example"))
		_, host, err = s.SearchHost(context.Background(), "alice", q)
		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(Equal("b.example"))
		Expect(b.calls).To(Equal(1))
	})

	It("cleans a result field for providers outside the package", func() {
		Expect(search.Clean("  a\n  b  ", 10)).To(Equal("a b"))
		Expect(search.Clean("abcdef", 3)).To(Equal("abc…"))
	})
})
