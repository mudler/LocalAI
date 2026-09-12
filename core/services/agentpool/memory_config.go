package agentpool

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/agents"

	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/xlog"
)

// wantsLongTermMemory reports whether the agent config asks for conversation
// persistence into the RAG/knowledge-base store.
func wantsLongTermMemory(cfg *state.AgentConfig) bool {
	return cfg != nil && (cfg.LongTermMemory || cfg.SummaryLongTermMemory)
}

// normalizeAgentMemoryConfig ensures long-term memory implies EnableKnowledgeBase.
// LocalAGI only attaches WithRAGDB when EnableKnowledgeBase is set, so enabling
// long_term_memory alone leaves ragdb nil and panics on save.
func normalizeAgentMemoryConfig(cfg *state.AgentConfig) {
	if !wantsLongTermMemory(cfg) {
		return
	}
	if !cfg.EnableKnowledgeBase {
		xlog.Warn("long_term_memory requires enable_kb to wire the RAG DB; enabling enable_kb automatically",
			"agent", cfg.Name)
		cfg.EnableKnowledgeBase = true
	}
}

// isUsableRAGDB reports whether db is a concrete store (not a degraded stub or
// unresolved recoverable wrapper). Used by create/update validation.
func isUsableRAGDB(db agent.RAGDB) bool {
	if db == nil {
		return false
	}
	if _, stub := db.(unavailableRAGDB); stub {
		return false
	}
	if _, lazy := db.(*recoverableRAGDB); lazy {
		return false
	}
	return true
}

// prepareAgentMemoryConfig normalizes memory flags and, when long-term memory is
// requested, verifies that a real (non-stub) RAG DB can be created. Returns
// ErrLongTermMemoryRequiresRAG when embedding/RAG is not configured.
func (s *AgentPoolService) prepareAgentMemoryConfig(userID string, cfg *state.AgentConfig) error {
	if cfg == nil {
		return nil
	}
	normalizeAgentMemoryConfig(cfg)
	if !wantsLongTermMemory(cfg) {
		return nil
	}

	if err := s.ensureCollectionForUser(userID, cfg.Name); err != nil {
		return fmt.Errorf("%w: %v", ErrLongTermMemoryRequiresRAG, err)
	}

	if s.ragFactory == nil {
		// Distributed / native executor path: no in-process LocalAGI RAG wiring.
		// Persistence goes through the HTTP KB API and fails soft; still require
		// that a collections backend exists so the misconfig is visible early.
		if s.collectionsBackend == nil {
			return fmt.Errorf("%w: collections backend not initialized", ErrLongTermMemoryRequiresRAG)
		}
		return nil
	}

	collection := agents.AgentKey(userID, cfg.Name)
	db, _, ok := s.ragFactory(collection)
	if !ok || !isUsableRAGDB(db) {
		return fmt.Errorf("%w: collection %q could not be initialized (install an embedding model or disable long_term_memory)",
			ErrLongTermMemoryRequiresRAG, collection)
	}
	return nil
}

// wrapRAGProvider returns a RAG provider that never yields a nil DB with ok=true.
// On a successful factory call the real DB is returned. On failure a recoverable
// lazy adapter is installed so LocalAGI's WithRAGDB still gets a non-nil DB
// (SIGSEGV safety) and later Store/Search/Reset/Count retry the factory once the
// embedding/vector store is back.
func wrapRAGProvider(
	inner func(collectionName string) (agent.RAGDB, state.KBCompactionClient, bool),
) func(collectionName, localRAGAPI, localRAGKey string) (agent.RAGDB, state.KBCompactionClient, bool) {
	return func(collectionName, _, _ string) (agent.RAGDB, state.KBCompactionClient, bool) {
		if inner != nil {
			if db, comp, ok := inner(collectionName); ok && isUsableRAGDB(db) {
				return db, comp, true
			}
		}
		xlog.Warn("RAG DB unavailable for collection; installing recoverable adapter to avoid long-term memory crash",
			"collection", collectionName)
		return newRecoverableRAGDB(collectionName, inner), nil, true
	}
}

// normalizeExistingLongTermMemoryAgents forces enable_kb on persisted agents that
// already have long-term memory enabled, then recreates them so LocalAGI wires
// WithRAGDB before the first chat. Must run after SetRAGProvider and before
// StartAll (RecreateAgent starts the agent; StartAll skips already-started ones).
func normalizeExistingLongTermMemoryAgents(pool *state.AgentPool) {
	if pool == nil {
		return
	}
	for _, name := range pool.List() {
		cfg := pool.GetConfig(name)
		if cfg == nil || !wantsLongTermMemory(cfg) || cfg.EnableKnowledgeBase {
			continue
		}
		cfg.EnableKnowledgeBase = true
		xlog.Warn("Recreating agent to wire RAG DB for long-term memory", "agent", name)
		if err := pool.RecreateAgent(name, cfg); err != nil {
			xlog.Error("Failed to recreate agent for long-term memory RAG wiring",
				"agent", name, "error", err)
		}
	}
}
