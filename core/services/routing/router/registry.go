package router

import (
	"sync"
)

// Registry is the process-wide store of built classifiers, keyed by
// router-model name. The middleware uses it to avoid rebuilding the
// score classifier on every request, and the admin status endpoint
// reads from it to surface per-classifier cache stats.
//
// Each entry carries the fingerprint of the RouterConfig it was built
// from. A Get() with a stale fingerprint reports a miss so the
// middleware rebuilds — matches the previous local-sync.Map behaviour
// that keyed on fingerprint alone.
type Registry struct {
	entries sync.Map // name → *registryEntry
}

type registryEntry struct {
	fingerprint uint64
	classifier  Classifier
}

func NewRegistry() *Registry { return &Registry{} }

// Get returns the cached classifier for the named router model iff the
// stored fingerprint matches. A miss (no entry, or stale fingerprint)
// returns false; the caller is expected to rebuild and Put the result.
func (r *Registry) Get(name string, fingerprint uint64) (Classifier, bool) {
	if r == nil {
		return nil, false
	}
	v, ok := r.entries.Load(name)
	if !ok {
		return nil, false
	}
	e := v.(*registryEntry)
	if e.fingerprint != fingerprint {
		return nil, false
	}
	return e.classifier, true
}

// Put stores a built classifier under (name, fingerprint), replacing
// any prior entry. The middleware calls this after a Get miss.
func (r *Registry) Put(name string, fingerprint uint64, c Classifier) {
	if r == nil {
		return
	}
	r.entries.Store(name, &registryEntry{fingerprint: fingerprint, classifier: c})
}

// EmbeddingCacheStatsByRouter returns a snapshot of every embedding
// cache currently in the registry, keyed by router-model name. Plain
// classifiers without the L2 cache wrapper are skipped — callers
// distinguish "cache disabled" from "cache enabled with zero hits" by
// the presence of the map key.
func (r *Registry) EmbeddingCacheStatsByRouter() map[string]EmbeddingCacheStats {
	if r == nil {
		return nil
	}
	out := map[string]EmbeddingCacheStats{}
	r.entries.Range(func(k, v any) bool {
		name, _ := k.(string)
		e, _ := v.(*registryEntry)
		if e == nil {
			return true
		}
		if ec, ok := e.classifier.(*EmbeddingCacheClassifier); ok {
			out[name] = ec.Stats()
		}
		return true
	})
	return out
}
