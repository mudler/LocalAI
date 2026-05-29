package prefixcache

import "time"

// Provider is the seam between SmartRouter and the prefix-cache implementation.
// The radix-tree (guessed) implementation is the only one today; a future
// KV-event (reported) implementation can satisfy the same interface without
// changing SmartRouter (epic #10063 / #10064).
type Provider interface {
	// Decide computes the prefix decision for a request given the candidate
	// node IDs (the selector-filtered set). It does not consult load - load
	// filtering happens in the DB transaction.
	Decide(model string, chain []uint64, candidateNodeIDs []string, now time.Time) PrefixDecision
	// Observe records that node served the request whose prefix is chain.
	// Returns true when the assignment was new or extended (caller broadcasts).
	Observe(model string, chain []uint64, nodeID string, now time.Time) bool
	// Invalidate drops all entries for (model, nodeID).
	Invalidate(model, nodeID string)
	// Evict sweeps expired entries for all models.
	Evict(now time.Time)
}
