package agentpool

import (
	"errors"
	"testing"

	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/state"
)

func TestNormalizeAgentMemoryConfigEnablesKB(t *testing.T) {
	cfg := &state.AgentConfig{Name: "a", LongTermMemory: true}
	normalizeAgentMemoryConfig(cfg)
	if !cfg.EnableKnowledgeBase {
		t.Fatal("expected EnableKnowledgeBase to be set when LongTermMemory is on")
	}
}

func TestNormalizeAgentMemoryConfigSummary(t *testing.T) {
	cfg := &state.AgentConfig{Name: "a", SummaryLongTermMemory: true}
	normalizeAgentMemoryConfig(cfg)
	if !cfg.EnableKnowledgeBase {
		t.Fatal("expected EnableKnowledgeBase to be set when SummaryLongTermMemory is on")
	}
}

func TestNormalizeAgentMemoryConfigNoopWithoutMemory(t *testing.T) {
	cfg := &state.AgentConfig{Name: "a"}
	normalizeAgentMemoryConfig(cfg)
	if cfg.EnableKnowledgeBase {
		t.Fatal("did not expect EnableKnowledgeBase without long-term memory")
	}
}

func TestWrapRAGProviderReturnsStubWhenInnerFails(t *testing.T) {
	inner := func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
		return nil, nil, false
	}
	db, _, ok := wrapRAGProvider(inner)("missing", "", "")
	if !ok {
		t.Fatal("expected ok=true from wrapped provider")
	}
	if db == nil {
		t.Fatal("expected non-nil stub RAGDB")
	}
	if _, isStub := db.(unavailableRAGDB); !isStub {
		t.Fatalf("expected unavailableRAGDB stub, got %T", db)
	}
	if err := db.Store("x"); err == nil {
		t.Fatal("expected stub Store to return an error")
	}
}

func TestWrapRAGProviderPassesThroughRealDB(t *testing.T) {
	real := unavailableRAGDB{reason: "not-a-stub-for-pass-through-test"}
	// Use a distinct concrete type so we can tell pass-through from the failure stub.
	innerDB := &countingRAGDB{}
	inner := func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
		return innerDB, nil, true
	}
	db, _, ok := wrapRAGProvider(inner)("ok", "", "")
	if !ok || db != innerDB {
		t.Fatalf("expected pass-through of real DB, ok=%v db=%T", ok, db)
	}
	_ = real
}

func TestPrepareAgentMemoryConfigRejectsMissingRAG(t *testing.T) {
	s := &AgentPoolService{
		ragFactory: func(string) (agent.RAGDB, state.KBCompactionClient, bool) {
			return nil, nil, false
		},
	}
	// Bypass ensureCollectionForUser by stubbing collectionsBackend path:
	// prepareAgentMemoryConfig calls ensureCollectionForUser first. With a nil
	// collectionsBackend that returns an error via CollectionsBackendForUser.
	cfg := &state.AgentConfig{Name: "agent", LongTermMemory: true}
	err := s.prepareAgentMemoryConfig("", cfg)
	if err == nil {
		t.Fatal("expected error when RAG cannot be initialized")
	}
	if !errors.Is(err, ErrLongTermMemoryRequiresRAG) {
		t.Fatalf("expected ErrLongTermMemoryRequiresRAG, got %v", err)
	}
	if !cfg.EnableKnowledgeBase {
		t.Fatal("expected normalize to enable KB even when prepare fails")
	}
}

type countingRAGDB struct{ n int }

func (c *countingRAGDB) Store(string) error                  { c.n++; return nil }
func (c *countingRAGDB) Reset() error                        { return nil }
func (c *countingRAGDB) Search(string, int) ([]string, error) { return nil, nil }
func (c *countingRAGDB) Count() int                          { return c.n }
