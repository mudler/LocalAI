// SPDX-License-Identifier: MIT
package search_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/pirateface-search/internal/search"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// hostBroker stands in for the add-on gateway: it binds the arguments to the
// schema of the manifest as the host does, then runs the real handler of the
// add-on, and returns the payload of its answer.
type hostBroker struct {
	rt  contract.Runtime
	def contract.ToolDefinition
	m   contract.ModelSearch
}

func (b hostBroker) Sources(context.Context, string) ([]modelsearch.AddonSource, error) {
	return []modelsearch.AddonSource{{AddonID: "pirateface-search", Tool: search.ToolSearch, Running: true, Search: b.m}}, nil
}

func (b hostBroker) Call(ctx context.Context, user, addonID, tool string, args json.RawMessage) (json.RawMessage, error) {
	_, bound, err := toolargs.BindDefinition(b.def, args)
	if err != nil {
		return nil, err
	}
	var e contract.Envelope = env(nil)
	e.Request.Arguments = bound
	e.Authority.Actor.UserID = user
	res, err := b.rt.Read(ctx, e)
	if err != nil {
		return nil, err
	}
	return res.Payload, nil
}

// The final check of the feature: the host's search, its enabled set and its
// row checks, with the add-on's own handler behind them, over a recorded page.
var _ = Describe("the add-on behind the host search", func() {
	It("merges Hugging Face and Pirate Face, passes the filter, and keeps the host-set fields", func() {
		raw, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(fixture()))
		}))
		DeferCleanup(site.Close)

		s := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: -1})
		s.Register(staticSource{name: "huggingface", rows: []modelsearch.Result{{ID: "o/hf", URI: "huggingface://o/hf"}}})
		broker := hostBroker{rt: search.New(search.Options{BaseURL: site.URL}), def: m.Capabilities.ToolDefinitions[0], m: *m.Capabilities.ModelSearch}
		h := &modelsearch.Host{Searcher: s, Sync: modelsearch.NewAddonSync(s, broker), Selection: modelsearch.NewSelection(&memKV{})}
		ctx := context.Background()

		Expect(h.SetEnabled(ctx, "ann", []string{"huggingface", "pirateface"})).To(Succeed())
		r := h.Search(ctx, "ann", modelsearch.Query{Text: "qwen", Filters: map[string]map[string]string{"pirateface": {"min-seeders": "2"}}}, nil)
		Expect(r.Providers).To(HaveLen(2))
		Expect(r.Providers[1]).To(Equal(modelsearch.ProviderStatus{Name: "pirateface", Title: "Pirate Face", OK: true, Count: 2}))
		Expect(r.Results).To(HaveLen(3))
		Expect(r.Results[1].Provider).To(Equal("pirateface"))
		Expect(r.Results[1].URI).To(Equal("pirateface://Qwen/Qwen3-0.6B"))
		Expect(r.Results[1].Magnet).To(HavePrefix("magnet:?xt=urn:btih:"))
		Expect(r.Sources[1].Filters[0].ID).To(Equal(search.FilterMinSeeders))

		// A filter value outside the declared range never reaches the add-on.
		bad := h.Search(ctx, "ann", modelsearch.Query{Text: "qwen", Filters: map[string]map[string]string{"pirateface": {"min-seeders": "900"}}}, []string{"pirateface"})
		Expect(bad.Providers[0].OK).To(BeFalse())
	})
})

type staticSource struct {
	name string
	rows []modelsearch.Result
}

func (s staticSource) Name() string { return s.name }
func (s staticSource) Search(context.Context, modelsearch.Query) ([]modelsearch.Result, error) {
	return s.rows, nil
}

type memKV struct{ v map[string]string }

func (m *memKV) Get(_ context.Context, u string) (string, bool, error) {
	v, ok := m.v[u]
	return v, ok, nil
}
func (m *memKV) Set(_ context.Context, u, v string) error {
	if m.v == nil {
		m.v = map[string]string{}
	}
	m.v[u] = v
	return nil
}
