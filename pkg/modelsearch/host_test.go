package modelsearch_test

import (
	stdctx "context"
	"encoding/json"
	"slices"
	gosync "sync"

	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The search host", func() {
	var (
		b  *fakeBroker
		kv *memKV
		h  *modelsearch.Host
	)
	BeforeEach(func() {
		b = &fakeBroker{answers: map[string]json.RawMessage{}, errs: map[string]error{}, block: map[string]bool{}}
		kv = &memKV{}
		s := modelsearch.NewSearcher(modelsearch.Options{})
		s.Register(&fakeProvider{name: "huggingface", res: []modelsearch.Result{{ID: "o/hf"}}})
		h = modelsearch.NewHost(s, b, kv)
		b.sources = []modelsearch.AddonSource{addonSource("pf", "pirateface")}
		b.answers["pf"] = rowsJSON(row("o/pf", "pirateface://o/pf"))
	})
	ctx := stdctx.Background()

	It("searches the built-in and the add-on source with no action, and each person turns one off for themselves", func() {
		r := h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil)
		Expect(r.Providers).To(HaveLen(2))
		Expect(r.Results).To(HaveLen(2))
		Expect(h.SetEnabled(ctx, "alice", []string{"huggingface"})).To(Succeed())
		r = h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil)
		Expect(r.Providers).To(HaveLen(1))
		Expect(r.Providers[0].Name).To(Equal("huggingface"))
		// Another person keeps every source.
		Expect(h.Search(ctx, "bob", modelsearch.Query{Text: "x"}, nil).Providers).To(HaveLen(2))
	})

	It("turns on a source installed after the person saved a choice", func() {
		b.sources = nil
		Expect(h.SetEnabled(ctx, "alice", []string{"huggingface"})).To(Succeed())
		b.sources = []modelsearch.AddonSource{addonSource("pf", "pirateface")}
		Expect(h.Enabled(ctx, "alice")).To(Equal([]string{"huggingface", "pirateface"}))
	})

	It("keeps a source off after the person turned it off, also when the add-on goes and comes back", func() {
		Expect(h.SetEnabled(ctx, "alice", []string{"huggingface"})).To(Succeed())
		saved := b.sources
		b.sources = nil
		o := h.Overview(ctx, "alice")
		Expect(o.Missing).To(BeEmpty())
		Expect(o.Sources).To(HaveLen(1))
		Expect(h.SetEnabled(ctx, "alice", o.Enabled)).To(Succeed())
		b.sources = saved
		Expect(h.Overview(ctx, "alice").Enabled).To(Equal([]string{"huggingface"}))
	})

	It("answers an empty set with nothing", func() {
		Expect(h.SetEnabled(ctx, "alice", nil)).To(Succeed())
		r := h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil)
		Expect(r.Results).To(BeEmpty())
		Expect(r.Providers).To(BeEmpty())
		Expect(h.Overview(ctx, "alice").Enabled).To(BeEmpty())
	})

	It("turns every visible source on when the store cannot be read", func() {
		kv.err = stdctx.DeadlineExceeded
		Expect(h.Enabled(ctx, "alice")).To(Equal([]string{"huggingface", "pirateface"}))
	})

	It("lists the sources with their title and filters", func() {
		o := h.Overview(ctx, "alice")
		Expect(o.Sources).To(HaveLen(2))
		Expect(o.Sources[1].ID).To(Equal("pirateface"))
		Expect(o.Sources[1].Kind).To(Equal("addon"))
	})

	It("keeps a source that one person can see when another person, who cannot, searches at the same time", func() {
		b.perUser = map[string][]modelsearch.AddonSource{"alice": {addonSource("pf", "pirateface")}, "bob": nil}
		Expect(h.Visible(ctx, "alice")).To(ContainElement("pirateface"))
		var wg gosync.WaitGroup
		failures := make(chan string, 200)
		for range 4 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for range 40 {
					r := h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, []string{"pirateface"})
					if len(r.Providers) != 1 || !r.Providers[0].OK {
						failures <- "alice: " + r.Providers[0].Error
					}
					if !slices.Contains(h.Visible(ctx, "alice"), "pirateface") {
						failures <- "alice does not see her source"
					}
				}
			}()
			go func() {
				defer wg.Done()
				for range 40 {
					if slices.Contains(h.Visible(ctx, "bob"), "pirateface") {
						failures <- "bob sees a source he is denied"
					}
					o := h.Overview(ctx, "bob")
					if len(o.Sources) != 1 {
						failures <- "bob overview flaps"
					}
				}
			}()
		}
		wg.Wait()
		close(failures)
		for f := range failures {
			Fail(f)
		}
		// Once nobody can see it, the source is gone.
		b.perUser = map[string][]modelsearch.AddonSource{"alice": nil, "bob": nil}
		Expect(h.Visible(ctx, "alice")).NotTo(ContainElement("pirateface"))
		Expect(h.Searcher.Has("pirateface")).To(BeFalse())
	})

	It("unregisters a source whose add-on was removed even when another person, who is idle, saw it last", func() {
		b.perUser = map[string][]modelsearch.AddonSource{"alice": {addonSource("pf", "pirateface")}, "bob": {addonSource("pf", "pirateface")}}
		Expect(h.Visible(ctx, "alice")).To(ContainElement("pirateface"))
		Expect(h.Visible(ctx, "bob")).To(ContainElement("pirateface"))
		// The add-on is uninstalled. Bob never searches again; alice does.
		b.perUser = map[string][]modelsearch.AddonSource{"alice": nil, "bob": nil}
		Expect(h.Visible(ctx, "alice")).NotTo(ContainElement("pirateface"))
		Expect(h.Searcher.Has("pirateface")).To(BeFalse())
		// A different add-on can now declare the same source ID.
		other := addonSource("other-addon", "pirateface")
		b.perUser = map[string][]modelsearch.AddonSource{"alice": {other}}
		Expect(h.Visible(ctx, "alice")).To(ContainElement("pirateface"))
		src, ok := h.Searcher.Source("pirateface")
		Expect(ok).To(BeTrue())
		Expect(src.Kind).To(Equal("addon"))
	})
})
