package modelsearch_test

import (
	stdctx "context"
	"encoding/json"

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

	It("searches only the built-in source until a person enables another", func() {
		r := h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil)
		Expect(r.Providers).To(HaveLen(1))
		Expect(r.Providers[0].Name).To(Equal("huggingface"))
		Expect(h.SetEnabled(ctx, "alice", []string{"huggingface", "pirateface"})).To(Succeed())
		r = h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil)
		Expect(r.Providers).To(HaveLen(2))
		Expect(r.Results).To(HaveLen(2))
		// Another person keeps the default.
		Expect(h.Search(ctx, "bob", modelsearch.Query{Text: "x"}, nil).Providers).To(HaveLen(1))
	})

	It("answers an empty set with nothing", func() {
		Expect(h.SetEnabled(ctx, "alice", nil)).To(Succeed())
		r := h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil)
		Expect(r.Results).To(BeEmpty())
		Expect(r.Providers).To(BeEmpty())
	})

	It("ignores and keeps an enabled source whose add-on is gone, and restores it on reinstall", func() {
		Expect(h.SetEnabled(ctx, "alice", []string{"huggingface", "pirateface"})).To(Succeed())
		saved := b.sources
		b.sources = nil
		o := h.Overview(ctx, "alice")
		Expect(o.Missing).To(Equal([]string{"pirateface"}))
		Expect(o.Sources).To(HaveLen(1))
		Expect(h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil).Providers).To(HaveLen(1))
		Expect(h.SetEnabled(ctx, "alice", o.Enabled)).To(Succeed())
		b.sources = saved
		o = h.Overview(ctx, "alice")
		Expect(o.Missing).To(BeEmpty())
		Expect(h.Search(ctx, "alice", modelsearch.Query{Text: "x"}, nil).Providers).To(HaveLen(2))
	})

	It("falls back to the default set when the store cannot be read", func() {
		kv.err = stdctx.DeadlineExceeded
		Expect(h.Enabled(ctx, "alice")).To(Equal([]string{"huggingface"}))
	})

	It("lists the sources with their title and filters", func() {
		o := h.Overview(ctx, "alice")
		Expect(o.Sources).To(HaveLen(2))
		Expect(o.Sources[1].ID).To(Equal("pirateface"))
		Expect(o.Sources[1].Kind).To(Equal("addon"))
	})
})
