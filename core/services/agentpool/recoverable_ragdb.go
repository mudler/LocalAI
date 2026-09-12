package agentpool

import (
	"sync"

	"github.com/mudler/LocalAGI/core/agent"
	"github.com/mudler/LocalAGI/core/state"
)

// recoverableRAGDB is a non-nil RAGDB that retries the inner factory until a
// real DB is obtained. LocalAGI stores the RAGDB from the provider for the
// life of the agent (WithRAGDB); returning a permanent unavailableRAGDB on a
// transient init failure would discard all long-term-memory writes until the
// agent is recreated. This adapter keeps retrying on Store/Search/Reset/Count
// so recovery happens without recreation (see mudler/LocalAI#11975 review).
type recoverableRAGDB struct {
	mu         sync.Mutex
	collection string
	factory    func(collectionName string) (agent.RAGDB, state.KBCompactionClient, bool)
	real       agent.RAGDB
}

var _ agent.RAGDB = (*recoverableRAGDB)(nil)

func newRecoverableRAGDB(
	collection string,
	factory func(collectionName string) (agent.RAGDB, state.KBCompactionClient, bool),
) *recoverableRAGDB {
	return &recoverableRAGDB{collection: collection, factory: factory}
}

// resolve returns a usable RAGDB. Once a real DB is obtained it is cached.
// Temporary factory failures keep returning the soft-fail stub so callers never
// see a nil DB (SIGSEGV safety). Permanent "not configured" vs transient
// failures cannot be distinguished from the factory's (nil, false) signal, so
// we always keep retrying.
func (r *recoverableRAGDB) resolve() agent.RAGDB {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.real != nil {
		return r.real
	}
	if r.factory != nil {
		if db, _, ok := r.factory(r.collection); ok && db != nil {
			switch db.(type) {
			case unavailableRAGDB, *recoverableRAGDB:
				// Never cache another degraded wrapper as "recovered".
			default:
				r.real = db
				return r.real
			}
		}
	}
	return unavailableRAGDB{reason: ragUnavailableReason}
}

func (r *recoverableRAGDB) Store(s string) error {
	return r.resolve().Store(s)
}

func (r *recoverableRAGDB) Reset() error {
	return r.resolve().Reset()
}

func (r *recoverableRAGDB) Search(s string, similarEntries int) ([]string, error) {
	return r.resolve().Search(s, similarEntries)
}

func (r *recoverableRAGDB) Count() int {
	return r.resolve().Count()
}
