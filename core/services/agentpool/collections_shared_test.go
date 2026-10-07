package agentpool

// White-box tests: two frontends share one registry (the database) and one bus,
// but each has its own in-memory collections cache, like two replicas behind
// one Service.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAGI/webui/collections"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/localrecall/rag"
	"github.com/mudler/localrecall/rag/sources"
)

// fakeRegistry stands in for the collection_config table.
type fakeRegistry struct {
	mu    sync.Mutex
	names map[string]bool
	err   error
}

func newFakeRegistry() *fakeRegistry { return &fakeRegistry{names: map[string]bool{}} }

func (r *fakeRegistry) add(n string)    { r.mu.Lock(); r.names[n] = true; r.mu.Unlock() }
func (r *fakeRegistry) remove(n string) { r.mu.Lock(); delete(r.names, n); r.mu.Unlock() }
func (r *fakeRegistry) setErr(e error)  { r.mu.Lock(); r.err = e; r.mu.Unlock() }

func (r *fakeRegistry) Exists(_ context.Context, n string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.names[n], r.err
}

func (r *fakeRegistry) List(_ context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	out := []string{}
	for n := range r.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// fakeInner models the in-process backend: it only knows the collections in
// its own cache and answers "collection not found" for the rest, which is the
// bug under test.
type fakeInner struct {
	state    *collections.State
	registry *fakeRegistry
	opens    atomic.Int32
	uploads  []string
	// resetDeletes models a store where a reset removes the registry row.
	resetDeletes bool
}

func newFakeInner(reg *fakeRegistry) *fakeInner {
	f := &fakeInner{
		registry: reg,
		state: &collections.State{
			Collections:   collections.CollectionList{},
			SourceManager: rag.NewSourceManager(&sources.Config{}),
		},
	}
	f.state.EnsureCollection = func(name string) (*rag.PersistentKB, bool) {
		f.opens.Add(1)
		f.state.Mu.Lock()
		defer f.state.Mu.Unlock()
		if kb := f.state.Collections[name]; kb != nil {
			return kb, true
		}
		kb := &rag.PersistentKB{}
		f.state.Collections[name] = kb
		return kb, true
	}
	return f
}

func (f *fakeInner) known(name string) error {
	f.state.Mu.RLock()
	defer f.state.Mu.RUnlock()
	if kb := f.state.Collections[name]; kb == nil {
		return fmt.Errorf("collection not found: %s", name)
	}
	return nil
}

func (f *fakeInner) ListCollections() ([]string, error) {
	f.state.Mu.RLock()
	defer f.state.Mu.RUnlock()
	out := []string{}
	for n := range f.state.Collections {
		out = append(out, n)
	}
	return out, nil
}

func (f *fakeInner) CreateCollection(name string) error {
	f.registry.add(name)
	f.state.EnsureCollection(name)
	return nil
}

func (f *fakeInner) Upload(c, name string, _ io.Reader) (string, error) {
	if err := f.known(c); err != nil {
		return "", err
	}
	f.uploads = append(f.uploads, name)
	return name, nil
}

func (f *fakeInner) ListEntries(c string) ([]string, error) {
	if err := f.known(c); err != nil {
		return nil, err
	}
	return f.uploads, nil
}

func (f *fakeInner) GetEntryContent(c, _ string) (string, int, error) {
	return "", 0, f.known(c)
}

func (f *fakeInner) Search(c, _ string, _ int) ([]collections.SearchResult, error) {
	return nil, f.known(c)
}

func (f *fakeInner) Reset(c string) error {
	if err := f.known(c); err != nil {
		return err
	}
	f.state.Mu.Lock()
	delete(f.state.Collections, c)
	f.state.Mu.Unlock()
	if f.resetDeletes {
		f.registry.remove(c)
	}
	return nil
}

func (f *fakeInner) DeleteEntry(c, _ string) ([]string, error) { return nil, f.known(c) }
func (f *fakeInner) AddSource(c, _ string, _ int) error        { return f.known(c) }
func (f *fakeInner) RemoveSource(c, _ string) error            { return f.known(c) }
func (f *fakeInner) ListSources(c string) ([]collections.SourceInfo, error) {
	return nil, f.known(c)
}
func (f *fakeInner) EntryExists(c, _ string) bool { return f.known(c) == nil }
func (f *fakeInner) GetEntryFilePath(c, _ string) (string, error) {
	return "", f.known(c)
}

var _ = Describe("sharedCollections across two frontends", func() {
	var (
		reg    *fakeRegistry
		bus    *testutil.FakeBus
		innerA *fakeInner
		innerB *fakeInner
		a, b   *sharedCollections
	)

	BeforeEach(func() {
		reg = newFakeRegistry()
		bus = testutil.NewFakeBus()
		innerA, innerB = newFakeInner(reg), newFakeInner(reg)
		a = newSharedCollections(innerA, innerA.state, reg, bus)
		b = newSharedCollections(innerB, innerB.state, reg, bus)
	})

	AfterEach(func() { a.Close(); b.Close() })

	It("fails without the shared layer: the bug", func() {
		Expect(innerA.CreateCollection("col-1")).To(Succeed())
		_, err := innerB.Upload("col-1", "f.txt", nil)
		Expect(err).To(MatchError(ContainSubstring("collection not found")))
	})

	It("serves a collection created on A through B", func() {
		Expect(a.CreateCollection("col-1")).To(Succeed())

		_, err := b.Upload("col-1", "f.txt", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = b.Search("col-1", "q", 3)
		Expect(err).NotTo(HaveOccurred())
		_, err = b.ListEntries("col-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(b.EntryExists("col-1", "f.txt")).To(BeTrue())
		Expect(b.Reset("col-1")).To(Succeed())
	})

	It("lists the same collections on both frontends", func() {
		Expect(a.CreateCollection("col-a")).To(Succeed())
		Expect(b.CreateCollection("col-b")).To(Succeed())

		la, err := a.ListCollections()
		Expect(err).NotTo(HaveOccurred())
		lb, err := b.ListCollections()
		Expect(err).NotTo(HaveOccurred())
		Expect(la).To(Equal([]string{"col-a", "col-b"}))
		Expect(lb).To(Equal(la))
	})

	It("answers not found for a collection that does not exist anywhere", func() {
		_, err := b.Upload("nope", "f.txt", nil)
		Expect(err).To(MatchError(ContainSubstring("collection not found: nope")))
		Expect(innerB.opens.Load()).To(BeZero(), "a miss must never create the collection")
	})

	It("answers not found on B after the collection is gone, without waiting for the re-check", func() {
		Expect(a.CreateCollection("col-1")).To(Succeed())
		_, err := b.Search("col-1", "q", 1)
		Expect(err).NotTo(HaveOccurred())

		// The collection leaves the shared registry; A tells the other frontends.
		innerA.resetDeletes = true
		Expect(a.Reset("col-1")).To(Succeed())

		_, err = b.Search("col-1", "q", 1)
		Expect(err).To(MatchError(ContainSubstring("collection not found")))
		Expect(innerB.known("col-1")).To(HaveOccurred(), "the stale cache entry is dropped")
	})

	It("notices a removal at the next re-check when no event arrives", func() {
		quiet := newSharedCollections(innerB, innerB.state, reg, nil)
		quiet.recheck = 0
		Expect(a.CreateCollection("col-1")).To(Succeed())
		_, err := quiet.Search("col-1", "q", 1)
		Expect(err).NotTo(HaveOccurred())

		reg.remove("col-1")
		_, err = quiet.Search("col-1", "q", 1)
		Expect(err).To(MatchError(ContainSubstring("collection not found")))
	})

	It("keeps serving the cached collection when the registry is unreachable", func() {
		Expect(a.CreateCollection("col-1")).To(Succeed())
		_, err := b.Search("col-1", "q", 1)
		Expect(err).NotTo(HaveOccurred())

		b.forget("col-1")
		reg.setErr(errors.New("connection refused"))
		_, err = b.Search("col-1", "q", 1)
		Expect(err).NotTo(HaveOccurred())

		_, err = b.ListCollections()
		Expect(err).To(HaveOccurred())
	})

	It("re-checks at once when an event arrives", func() {
		Expect(a.CreateCollection("col-1")).To(Succeed())
		_, err := b.Search("col-1", "q", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.fresh("col-1")).To(BeTrue())

		Expect(a.Reset("col-1")).To(Succeed())
		Expect(b.fresh("col-1")).To(BeFalse())
		Expect(bus.PublishCount("cache.invalidate.collections.col-1")).To(Equal(2), "create and reset")
	})

	It("opens a collection once for concurrent first lookups", func() {
		Expect(a.CreateCollection("col-1")).To(Succeed())

		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, err := b.Search("col-1", "q", 1)
				Expect(err).NotTo(HaveOccurred())
			}()
		}
		wg.Wait()
		Expect(innerB.opens.Load()).To(Equal(int32(1)))
	})

	It("works without a bus", func() {
		c := newSharedCollections(innerB, innerB.state, reg, nil)
		Expect(a.CreateCollection("col-1")).To(Succeed())
		_, err := c.Upload("col-1", "f.txt", nil)
		Expect(err).NotTo(HaveOccurred())
		c.Close()
	})
})
