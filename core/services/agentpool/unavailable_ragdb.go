package agentpool

import (
	"fmt"

	"github.com/mudler/LocalAGI/core/agent"
)

// unavailableRAGDB is a non-nil RAGDB that fails soft on every operation.
// LocalAGI's saveCurrentConversation calls ragdb.Store when long-term memory is
// enabled without nil-checking; returning this stub from the RAG provider
// prevents a process-wide SIGSEGV when the embedding/RAG DB cannot be wired
// (see mudler/LocalAI#11975 / mudler/LocalAGI#405).
type unavailableRAGDB struct {
	reason string
}

var _ agent.RAGDB = unavailableRAGDB{}

func (u unavailableRAGDB) Store(string) error {
	return fmt.Errorf("RAG DB unavailable: %s", u.reason)
}

func (u unavailableRAGDB) Reset() error {
	return fmt.Errorf("RAG DB unavailable: %s", u.reason)
}

func (u unavailableRAGDB) Search(string, int) ([]string, error) {
	return nil, fmt.Errorf("RAG DB unavailable: %s", u.reason)
}

func (u unavailableRAGDB) Count() int { return 0 }

const ragUnavailableReason = "embedding model / vector store not configured or failed to initialize"
