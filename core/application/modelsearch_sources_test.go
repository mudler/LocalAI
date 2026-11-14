package application

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// searchOwner is the owner side behind the broker: it lists one add-on that
// provides the model-search slot and answers its search tool.
type searchOwner struct {
	mu         sync.Mutex
	app        apps.App
	dispatched []contract.Request
}

func (o *searchOwner) Apps(context.Context, contract.Actor) (apps.List, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return apps.List{Apps: []apps.App{o.app}}, nil
}

func (o *searchOwner) Dispatch(_ context.Context, _ contract.Actor, r contract.Request) (contract.Result, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dispatched = append(o.dispatched, r)
	return contract.Result{Payload: json.RawMessage(`{"results":[{"id":"acme/Peer","uri":"pirateface://acme/Peer","magnet":"magnet:?xt=urn:btih:abc","seeders":6}]}`)}, nil
}

func (o *searchOwner) Propose(context.Context, contract.Actor, contract.Request) (gateway.Proposal, error) {
	return gateway.Proposal{}, gateway.ErrForbidden
}

type stringKV struct {
	mu sync.Mutex
	v  map[string]string
}

func (k *stringKV) Get(_ context.Context, u string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.v[u]
	return v, ok, nil
}

func (k *stringKV) Set(_ context.Context, u, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.v == nil {
		k.v = map[string]string{}
	}
	k.v[u] = v
	return nil
}

type builtinStub struct{}

func (builtinStub) Name() string { return "huggingface" }
func (builtinStub) Search(context.Context, modelsearch.Query) ([]modelsearch.Result, error) {
	return []modelsearch.Result{{ID: "o/hf", URI: "huggingface://o/hf"}}, nil
}

// The backend of the model search goes through the real chats broker and the
// real tool adapter (CallProvider, then Execute, then the owner), with a fake
// owner behind them. The enabled set decides who is searched; the choice of one
// provider for a slot (studio.Service) plays no part for a set slot.
var _ = Describe("the model search backend over the chats broker", func() {
	var (
		owner *searchOwner
		host  *modelsearch.Host
		ctx   = context.Background()
	)
	BeforeEach(func() {
		raw, err := os.ReadFile("../../addons/pirateface-search/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		owner = &searchOwner{app: apps.App{ID: "pirateface-search", Name: "pirateface-search", Generation: 1, Enabled: true, State: "running",
			Tools: m.Capabilities.Tools, ToolDefinitions: m.Capabilities.ToolDefinitions, Provides: m.Capabilities.Provides, ModelSearch: m.Capabilities.ModelSearch}}
		broker := func() studio.AddonTools {
			return &studioAddons{adapter: addontools.NewChatToolAdapter(owner), mode: auth.AddonModeLocalOwner, ids: func() []string { return nil }}
		}
		s := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: -1})
		s.Register(builtinStub{})
		host = modelsearch.NewHost(s, modelSearchBackend{broker: broker}, &stringKV{})
	})

	It("returns the rows of the add-on with no action of the person: its source is on by default", func() {
		r := host.Search(ctx, "ann", modelsearch.Query{Text: "qwen", Filters: map[string]map[string]string{"pirateface": {"min-seeders": "2"}}}, nil)
		Expect(r.Providers).To(HaveLen(2))
		Expect(r.Providers[1].OK).To(BeTrue(), r.Providers[1].Error)
		Expect(r.Results).To(HaveLen(2))
		Expect(r.Results[1].Provider).To(Equal("pirateface"))
		Expect(r.Results[1].HasVariants).To(BeTrue(), "the hint comes from the source description")
		Expect(r.Sources[1].P2P).To(BeTrue())
		Expect(owner.dispatched).To(HaveLen(1))
		var sent map[string]any
		Expect(json.Unmarshal(owner.dispatched[0].Arguments, &sent)).To(Succeed())
		Expect(sent).To(HaveKeyWithValue("tool", "search"))
		Expect(sent).To(HaveKeyWithValue("q", "qwen"))
		Expect(sent["filters"]).To(Equal(map[string]any{"min-seeders": float64(2)}))
	})

	It("returns nothing from the add-on, and does not call it, when the person turned its source off", func() {
		Expect(host.SetEnabled(ctx, "ann", []string{"huggingface"})).To(Succeed())
		r := host.Search(ctx, "ann", modelsearch.Query{Text: "qwen"}, nil)
		Expect(r.Providers).To(HaveLen(1))
		Expect(r.Providers[0].Name).To(Equal("huggingface"))
		Expect(owner.dispatched).To(BeEmpty())
		Expect(host.Overview(ctx, "ann").Sources).To(HaveLen(2), "it is installed, so the person can turn it back on")
	})

	It("does not wait for a provider pick: no choice is stored for the slot, and the set still works", func() {
		off, err := host.Selection.Off(ctx, "ann")
		Expect(err).NotTo(HaveOccurred())
		Expect(off).To(BeEmpty())
		Expect(host.SetEnabled(ctx, "ann", []string{"pirateface"})).To(Succeed())
		r := host.Search(ctx, "ann", modelsearch.Query{Text: "qwen"}, nil)
		Expect(r.Providers).To(HaveLen(1))
		Expect(r.Providers[0].OK).To(BeTrue(), r.Providers[0].Error)
	})

	It("reports a failed source when the add-on is stopped, and keeps the other source", func() {
		owner.mu.Lock()
		owner.app.State = "stopped"
		owner.mu.Unlock()
		r := host.Search(ctx, "ann", modelsearch.Query{Text: "qwen"}, nil)
		Expect(r.Providers).To(HaveLen(2))
		Expect(r.Providers[0].OK).To(BeTrue())
		Expect(r.Providers[1].OK).To(BeFalse())
		Expect(owner.dispatched).To(BeEmpty())
	})
})
