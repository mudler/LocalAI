package agentpool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/state"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	"github.com/mudler/LocalAGI/webui/collections"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeCollectionsBackend is enough for ensureCollectionForUser to succeed so
// prepareAgentMemoryConfig can reach the ragFactory rejection branch.
type fakeCollectionsBackend struct {
	listed []string
}

var _ collections.Backend = (*fakeCollectionsBackend)(nil)

func (f *fakeCollectionsBackend) ListCollections() ([]string, error) { return f.listed, nil }
func (f *fakeCollectionsBackend) CreateCollection(name string) error {
	f.listed = append(f.listed, name)
	return nil
}
func (f *fakeCollectionsBackend) Upload(string, string, io.Reader) (string, error) {
	return "", nil
}
func (f *fakeCollectionsBackend) ListEntries(string) ([]string, error) { return nil, nil }
func (f *fakeCollectionsBackend) GetEntryContent(string, string) (string, int, error) {
	return "", 0, nil
}
func (f *fakeCollectionsBackend) Search(string, string, int) ([]collections.SearchResult, error) {
	return nil, nil
}
func (f *fakeCollectionsBackend) Reset(string) error { return nil }
func (f *fakeCollectionsBackend) DeleteEntry(string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeCollectionsBackend) AddSource(string, string, int) error { return nil }
func (f *fakeCollectionsBackend) RemoveSource(string, string) error   { return nil }
func (f *fakeCollectionsBackend) ListSources(string) ([]collections.SourceInfo, error) {
	return nil, nil
}
func (f *fakeCollectionsBackend) EntryExists(string, string) bool { return false }
func (f *fakeCollectionsBackend) GetEntryFilePath(string, string) (string, error) {
	return "", errors.New("not found")
}

type countingRAGDB struct{ n atomic.Int32 }

func (c *countingRAGDB) Store(string) error {
	c.n.Add(1)
	return nil
}
func (c *countingRAGDB) Reset() error                         { return nil }
func (c *countingRAGDB) Search(string, int) ([]string, error) { return nil, nil }
func (c *countingRAGDB) Count() int                           { return int(c.n.Load()) }

var _ = Describe("agent memory config", func() {
	Describe("normalizeAgentMemoryConfig", func() {
		It("enables enable_kb when long_term_memory is on", func() {
			cfg := &state.AgentConfig{Name: "a", LongTermMemory: true}
			normalizeAgentMemoryConfig(cfg)
			Expect(cfg.EnableKnowledgeBase).To(BeTrue())
		})

		It("enables enable_kb when summary_long_term_memory is on", func() {
			cfg := &state.AgentConfig{Name: "a", SummaryLongTermMemory: true}
			normalizeAgentMemoryConfig(cfg)
			Expect(cfg.EnableKnowledgeBase).To(BeTrue())
		})

		It("leaves enable_kb alone without long-term memory", func() {
			cfg := &state.AgentConfig{Name: "a"}
			normalizeAgentMemoryConfig(cfg)
			Expect(cfg.EnableKnowledgeBase).To(BeFalse())
		})
	})

	Describe("wrapRAGProvider", func() {
		It("passes through a real DB when the factory succeeds", func() {
			innerDB := &countingRAGDB{}
			inner := func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
				return innerDB, nil, true
			}
			db, _, ok := wrapRAGProvider(inner)("ok", "", "")
			Expect(ok).To(BeTrue())
			Expect(db).To(BeIdenticalTo(innerDB))
		})

		It("returns a non-nil recoverable adapter when the factory fails", func() {
			inner := func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
				return nil, nil, false
			}
			db, _, ok := wrapRAGProvider(inner)("missing", "", "")
			Expect(ok).To(BeTrue())
			Expect(db).NotTo(BeNil())
			_, isLazy := db.(*recoverableRAGDB)
			Expect(isLazy).To(BeTrue())
			Expect(db.Store("x")).To(HaveOccurred())
		})

		It("retries the factory on Store after a failed init (failure-then-recovery)", func() {
			var calls atomic.Int32
			real := &countingRAGDB{}
			inner := func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
				n := calls.Add(1)
				if n == 1 {
					return nil, nil, false
				}
				return real, nil, true
			}

			db, _, ok := wrapRAGProvider(inner)("recover", "", "")
			Expect(ok).To(BeTrue())
			Expect(calls.Load()).To(Equal(int32(1)), "wrap probes the factory once")
			Expect(db).NotTo(BeIdenticalTo(real))

			Expect(db.Store("hello")).To(Succeed())
			Expect(calls.Load()).To(Equal(int32(2)), "Store must retry the factory after recovery")
			Expect(real.Count()).To(Equal(1))

			// Subsequent ops use the cached real DB without another factory call.
			Expect(db.Store("again")).To(Succeed())
			Expect(calls.Load()).To(Equal(int32(2)))
			Expect(real.Count()).To(Equal(2))
		})

		It("never returns nil DB with ok=true when inner is nil", func() {
			db, _, ok := wrapRAGProvider(nil)("x", "", "")
			Expect(ok).To(BeTrue())
			Expect(db).NotTo(BeNil())
			Expect(db.Store("x")).To(HaveOccurred())
		})
	})

	Describe("prepareAgentMemoryConfig", func() {
		It("rejects when ragFactory cannot initialize (not just missing backend)", func() {
			var factoryCalls atomic.Int32
			s := &AgentPoolService{
				collectionsBackend: &fakeCollectionsBackend{},
				ragFactory: func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
					factoryCalls.Add(1)
					return nil, nil, false
				},
			}
			cfg := &state.AgentConfig{Name: "agent", LongTermMemory: true}
			err := s.prepareAgentMemoryConfig("", cfg)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrLongTermMemoryRequiresRAG)).To(BeTrue())
			Expect(factoryCalls.Load()).To(BeNumerically(">=", 1), "must reach ragFactory rejection branch")
			Expect(cfg.EnableKnowledgeBase).To(BeTrue())
		})

		It("accepts when ragFactory returns a real DB", func() {
			s := &AgentPoolService{
				collectionsBackend: &fakeCollectionsBackend{listed: []string{"agent"}},
				ragFactory: func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
					return &countingRAGDB{}, nil, true
				},
			}
			cfg := &state.AgentConfig{Name: "agent", LongTermMemory: true}
			Expect(s.prepareAgentMemoryConfig("", cfg)).To(Succeed())
			Expect(cfg.EnableKnowledgeBase).To(BeTrue())
		})
	})

	Describe("normalizeExistingLongTermMemoryAgents", func() {
		It("forces enable_kb on persisted LTM agents and recreates them", func() {
			tmp := GinkgoT().TempDir()
			poolFile := filepath.Join(tmp, "pool.json")
			seed := map[string]state.AgentConfig{
				"ltm": {
					Name:                  "ltm",
					Model:                 "dummy",
					LongTermMemory:        true,
					EnableKnowledgeBase:   false,
					PeriodicRuns:          "10m",
					SchedulerPollInterval: "30s",
				},
			}
			data, err := json.Marshal(seed)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(poolFile, data, 0o644)).To(Succeed())

			noopActions := func(*state.AgentConfig) func(context.Context, *state.AgentPool) []coreTypes.Action {
				return func(context.Context, *state.AgentPool) []coreTypes.Action { return nil }
			}
			noopConnectors := func(*state.AgentConfig) []state.Connector { return nil }
			noopPrompts := func(*state.AgentConfig) func(context.Context, *state.AgentPool) []agent.DynamicPrompt {
				return func(context.Context, *state.AgentPool) []agent.DynamicPrompt { return nil }
			}
			noopFilters := func(*state.AgentConfig) coreTypes.JobFilters { return nil }

			pool, err := state.NewAgentPool(
				"dummy", "", "", "", "",
				"http://127.0.0.1:1", "",
				tmp,
				noopActions, noopConnectors, noopPrompts, noopFilters,
				"1s", false, nil, state.PoolLimits{},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.List()).To(ContainElement("ltm"))
			Expect(pool.GetConfig("ltm").EnableKnowledgeBase).To(BeFalse())

			// RecreateAgent may fail to fully start without a live LLM; the
			// important part is enable_kb is forced and persisted on the pool.
			normalizeExistingLongTermMemoryAgents(pool)

			cfg := pool.GetConfig("ltm")
			Expect(cfg).NotTo(BeNil())
			Expect(cfg.EnableKnowledgeBase).To(BeTrue())
		})
	})
})
