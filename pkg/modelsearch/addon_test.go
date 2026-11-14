package modelsearch_test

import (
	stdctx "context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeBroker stands in for the add-on gateway. Each add-on answers with a
// canned payload, an error or a block.
type fakeBroker struct {
	mu      sync.Mutex
	sources []modelsearch.AddonSource
	listErr error
	answers map[string]json.RawMessage
	errs    map[string]error
	block   map[string]bool
	calls   []fakeCall
}

type fakeCall struct {
	User, Addon, Tool string
	Req               contract.ModelSearchRequest
}

func (f *fakeBroker) Sources(_ stdctx.Context, _ string) ([]modelsearch.AddonSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]modelsearch.AddonSource(nil), f.sources...), f.listErr
}

func (f *fakeBroker) Call(ctx stdctx.Context, user, addon, tool string, args json.RawMessage) (json.RawMessage, error) {
	var req contract.ModelSearchRequest
	_ = json.Unmarshal(args, &req)
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{user, addon, tool, req})
	f.mu.Unlock()
	if f.block[addon] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := f.errs[addon]; err != nil {
		return nil, err
	}
	return f.answers[addon], nil
}

func (f *fakeBroker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func addonSource(addon, id string, filters ...contract.SearchFilter) modelsearch.AddonSource {
	return modelsearch.AddonSource{AddonID: addon, Tool: "search", Running: true, Search: contract.ModelSearch{
		Source:  contract.ModelSearchSource{ID: id, Title: strings.ToUpper(id[:1]) + id[1:]},
		Sorts:   []string{"downloads"},
		Filters: filters,
	}}
}

func rowsJSON(rows ...map[string]any) json.RawMessage {
	if rows == nil {
		rows = []map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"results": rows})
	return b
}

func row(id, uri string) map[string]any {
	return map[string]any{"id": id, "title": id, "uri": uri, "seeders": 3}
}

var kindFilter = contract.SearchFilter{ID: "kind", Type: contract.FilterEnum, Label: "Kind", Values: []string{"a", "b"}, Default: "a"}

var _ = Describe("Add-on sources", func() {
	var (
		b    *fakeBroker
		s    *modelsearch.Searcher
		sync *modelsearch.AddonSync
		hf   *fakeProvider
	)
	ctx := func() stdctx.Context { return modelsearch.WithUser(stdctx.Background(), "alice") }

	BeforeEach(func() {
		b = &fakeBroker{answers: map[string]json.RawMessage{}, errs: map[string]error{}, block: map[string]bool{}}
		s = modelsearch.NewSearcher(modelsearch.Options{Timeout: 150 * time.Millisecond})
		hf = &fakeProvider{name: "huggingface", res: []modelsearch.Result{{ID: "o/hf"}}}
		s.Register(hf)
		sync = modelsearch.NewAddonSync(s, b)
	})

	It("registers a running add-on's source and calls its search tool as the person", func() {
		b.sources = []modelsearch.AddonSource{addonSource("pf-addon", "pirateface", kindFilter)}
		b.answers["pf-addon"] = rowsJSON(row("o/r", "pirateface://o/r"))
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		Expect(s.Names()).To(Equal([]string{"huggingface", "pirateface"}))

		r := s.Search(ctx(), modelsearch.Query{Text: "qwen", Limit: 5, Sort: "likes",
			Filters: map[string]map[string]string{"pirateface": {"kind": "b"}}}, []string{"pirateface"})
		Expect(r.Providers).To(Equal([]modelsearch.ProviderStatus{{Name: "pirateface", Title: "Pirateface", OK: true, Count: 1}}))
		Expect(r.Results).To(HaveLen(1))
		Expect(r.Results[0].Provider).To(Equal("pirateface"))
		Expect(r.Sources).To(HaveLen(1))
		Expect(r.Sources[0].Filters).To(HaveLen(1))
		Expect(b.calls[0]).To(Equal(fakeCall{User: "alice", Addon: "pf-addon", Tool: "search",
			Req: contract.ModelSearchRequest{Query: "qwen", Limit: 5, Sort: "likes", Format: "gguf", Filters: map[string]any{"kind": "b"}}}))
	})

	It("keeps the other sources when one fails, times out or answers badly", func() {
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one"), addonSource("a2", "two"), addonSource("a3", "three"), addonSource("a4", "four")}
		b.answers["a1"] = rowsJSON(row("o/one", "https://example.org/one"))
		b.errs["a2"] = errors.New("upstream down")
		b.block["a3"] = true
		b.answers["a4"] = json.RawMessage(`not json`)
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		r := s.Search(ctx(), modelsearch.Query{Text: "x"}, []string{"huggingface", "one", "two", "three", "four"})
		ok := map[string]bool{}
		for _, p := range r.Providers {
			ok[p.Name] = p.OK
		}
		Expect(ok).To(Equal(map[string]bool{"huggingface": true, "one": true, "two": false, "three": false, "four": false}))
		Expect(r.Results).To(HaveLen(2))
		Expect(r.Providers[2].Error).To(ContainSubstring("could not search"))
		Expect(r.Providers[3].Error).To(ContainSubstring("deadline"))
		Expect(r.Providers[4].Error).To(ContainSubstring("not a list"))
	})

	It("reports a stopped add-on as a failed source", func() {
		src := addonSource("a1", "one")
		src.Running = false
		b.sources = []modelsearch.AddonSource{src}
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		r := s.Search(ctx(), modelsearch.Query{Text: "x"}, []string{"one", "huggingface"})
		Expect(r.Providers[0].OK).To(BeFalse())
		Expect(r.Providers[0].Error).To(ContainSubstring("not running"))
		Expect(r.Providers[1].OK).To(BeTrue())
		Expect(b.callCount()).To(BeZero())
	})

	It("checks every row and counts the dropped ones", func() {
		long := strings.Repeat("a", 201)
		tags := make([]string, 33)
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one")}
		b.answers["a1"] = rowsJSON(
			row("o/ok", "huggingface://o/ok"),
			row("o/magnet", "magnet:?xt=urn:btih:abc"),
			row("o/bad-scheme", "ftp://example.org/x"),
			row("o/file", "file:///etc/passwd"),
			row("o/js", "javascript:alert(1)"),
			row(long, "https://example.org/x"),
			map[string]any{"id": "o/tags", "uri": "https://example.org/t", "tags": tags},
			map[string]any{"id": "o/longtag", "uri": "https://example.org/t", "tags": []string{strings.Repeat("t", 65)}},
			map[string]any{"id": "o/url", "uri": "https://example.org/t", "url": "http://plain.example.org"},
			map[string]any{"id": "o/title", "uri": "https://example.org/t", "title": strings.Repeat("t", 301)},
		)
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		r := s.Search(ctx(), modelsearch.Query{Text: "x"}, []string{"one"})
		Expect(r.Providers[0].Count).To(Equal(2))
		Expect(r.Providers[0].Dropped).To(Equal(8))
		Expect(r.Results[0].ID).To(Equal("o/ok"))
	})

	It("sets the provider itself and discards the fields the host owns", func() {
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one")}
		b.answers["a1"] = rowsJSON(map[string]any{"id": "o/r", "uri": "https://example.org/r", "provider": "huggingface",
			"installed": true, "installed_name": "mine", "backend": "llama-cpp", "has_variants": true, "downloads": -4})
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		r := s.Search(ctx(), modelsearch.Query{Text: "x"}, []string{"one"})
		got := r.Results[0]
		Expect(got.Provider).To(Equal("one"))
		Expect(got.Installed).To(BeFalse())
		Expect(got.InstalledName).To(BeEmpty())
		Expect(got.Backend).To(BeEmpty())
		Expect(got.HasVariants).To(BeFalse())
		Expect(got.Downloads).To(BeZero())
	})

	It("reads at most MaxLimit rows", func() {
		var rows []map[string]any
		for i := range 80 {
			rows = append(rows, row(fmt.Sprintf("o/r%d", i), "https://example.org/r"))
		}
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one")}
		b.answers["a1"] = rowsJSON(rows...)
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		r := s.Search(ctx(), modelsearch.Query{Text: "x", Limit: 50}, []string{"one"})
		Expect(r.Providers[0].Count).To(Equal(modelsearch.MaxLimit))
	})

	It("refuses a filter that is not declared or does not fit, and does not call the add-on", func() {
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one", kindFilter)}
		b.answers["a1"] = rowsJSON()
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		src, _ := s.Source("one")
		_, err := modelsearch.ValidateFilters(src, map[string]string{"nope": "x"})
		var fe *modelsearch.FilterError
		Expect(errors.As(err, &fe)).To(BeTrue())
		Expect(fe.Filter).To(Equal("nope"))
		_, err = modelsearch.ValidateFilters(src, map[string]string{"kind": "z"})
		Expect(err).To(MatchError(ContainSubstring(`filter "kind"`)))
		r := s.Search(ctx(), modelsearch.Query{Text: "x", Filters: map[string]map[string]string{"one": {"kind": "z"}}}, []string{"one"})
		Expect(r.Providers[0].OK).To(BeFalse())
		Expect(b.callCount()).To(BeZero())
	})

	It("checks the value of each filter type", func() {
		src := modelsearch.Source{ID: "s", Filters: []contract.SearchFilter{
			{ID: "on", Type: contract.FilterBool, Label: "On"},
			{ID: "n", Type: contract.FilterRange, Label: "N", Min: 0, Max: 10},
			{ID: "t", Type: contract.FilterText, Label: "T", MaxLength: 5},
		}}
		out, err := modelsearch.ValidateFilters(src, map[string]string{"on": "true", "n": " 3.50 ", "t": "abc"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(map[string]string{"on": "true", "n": "3.5", "t": "abc"}))
		for _, bad := range []map[string]string{{"on": "maybe"}, {"n": "11"}, {"n": "x"}, {"n": "NaN"}, {"t": "abcdef"}} {
			_, err := modelsearch.ValidateFilters(src, bad)
			Expect(err).To(HaveOccurred(), fmt.Sprint(bad))
		}
	})

	It("caches by source and by filters", func() {
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one", kindFilter), addonSource("a2", "two", kindFilter)}
		b.answers["a1"], b.answers["a2"] = rowsJSON(), rowsJSON()
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		ask := func(source, kind string) modelsearch.Response {
			return s.Search(ctx(), modelsearch.Query{Text: "x", Filters: map[string]map[string]string{source: {"kind": kind}}}, []string{source})
		}
		Expect(ask("one", "a").Providers[0].Cached).To(BeFalse())
		Expect(ask("one", "a").Providers[0].Cached).To(BeTrue())
		Expect(ask("one", "b").Providers[0].Cached).To(BeFalse())
		Expect(ask("two", "a").Providers[0].Cached).To(BeFalse())
		Expect(b.callCount()).To(Equal(3))
	})

	It("removes the source of an add-on that is gone, and keeps one that only stopped", func() {
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one")}
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		Expect(s.Has("one")).To(BeTrue())
		b.sources = nil
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		Expect(s.Has("one")).To(BeFalse())
		Expect(s.Has("huggingface")).To(BeTrue())
	})

	It("refuses a second add-on with the same source ID, and a source ID of a built-in source", func() {
		b.sources = []modelsearch.AddonSource{addonSource("first", "dup"), addonSource("second", "dup"), addonSource("third", "huggingface")}
		b.answers["first"] = rowsJSON(row("o/first", "https://example.org/f"))
		b.answers["second"] = rowsJSON(row("o/second", "https://example.org/s"))
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		r := s.Search(ctx(), modelsearch.Query{Text: "x"}, []string{"dup", "huggingface"})
		Expect(r.Results[0].ID).To(Equal("o/first"))
		Expect(r.Results[1].ID).To(Equal("o/hf"))
	})

	It("reports a broker that cannot list the add-ons", func() {
		b.listErr = errors.New("boom")
		Expect(sync.Sync(ctx(), "alice")).To(MatchError(ContainSubstring("list the add-on sources")))
	})

	It("lists no latest models for a source that cannot", func() {
		b.sources = []modelsearch.AddonSource{addonSource("a1", "one")}
		Expect(sync.Sync(ctx(), "alice")).To(Succeed())
		r := s.Search(ctx(), modelsearch.Query{Sort: modelsearch.SortLatest}, []string{"one"})
		Expect(r.Providers[0].Unsupported).To(BeTrue())
		Expect(b.callCount()).To(BeZero())
	})
})

type memKV struct {
	mu  sync.Mutex
	val map[string]string
	err error
}

func (m *memKV) Get(_ stdctx.Context, u string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.val[u]
	return v, ok, m.err
}

func (m *memKV) Set(_ stdctx.Context, u, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.val == nil {
		m.val = map[string]string{}
	}
	m.val[u] = v
	return nil
}

var _ = Describe("The enabled set of sources", func() {
	var kv *memKV
	var sel *modelsearch.Selection
	BeforeEach(func() { kv = &memKV{}; sel = modelsearch.NewSelection(kv) })

	It("defaults to the built-in source", func() {
		got, err := sel.Enabled(context(), "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"huggingface"}))
	})

	It("round trips a set, and an empty set is a choice", func() {
		Expect(sel.Set(context(), "alice", []string{"builtin", "pirateface", "pirateface"}, []string{"pirateface"})).To(Succeed())
		got, _ := sel.Enabled(context(), "alice")
		Expect(got).To(Equal([]string{"huggingface", "pirateface"}))
		Expect(sel.Set(context(), "alice", nil, nil)).To(Succeed())
		got, _ = sel.Enabled(context(), "alice")
		Expect(got).To(BeEmpty())
	})

	It("refuses an ID that is not installed and not stored", func() {
		Expect(sel.Set(context(), "alice", []string{"ghost"}, nil)).To(MatchError(modelsearch.ErrUnknownSource))
		Expect(sel.Set(context(), "alice", []string{"Bad Name"}, nil)).To(MatchError(modelsearch.ErrUnknownSource))
	})

	It("keeps a stored ID whose add-on is gone, so a reinstall restores it", func() {
		Expect(sel.Set(context(), "alice", []string{"huggingface", "pirateface"}, []string{"pirateface"})).To(Succeed())
		// The add-on is removed. The person saves a change: the ID stays.
		Expect(sel.Set(context(), "alice", []string{"huggingface", "pirateface"}, nil)).To(Succeed())
		got, _ := sel.Enabled(context(), "alice")
		Expect(got).To(ContainElement("pirateface"))
	})

	It("keeps the choice of one person apart from another", func() {
		Expect(sel.Set(context(), "alice", nil, nil)).To(Succeed())
		got, _ := sel.Enabled(context(), "bob")
		Expect(got).To(Equal([]string{"huggingface"}))
	})

	It("reports a store that cannot answer", func() {
		kv.err = errors.New("down")
		_, err := sel.Enabled(context(), "alice")
		Expect(err).To(HaveOccurred())
	})
})
