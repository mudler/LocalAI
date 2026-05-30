package prefixcache

import "time"

// Provider is the seam between SmartRouter and the prefix-cache implementation.
// The radix-tree (guessed) implementation is the only one today; a future
// KV-event (reported) implementation can satisfy the same interface without
// changing SmartRouter (epic #10063 / #10064). Affinity is tracked per replica:
// each loaded replica is a separate process with its own KV cache.
type Provider interface {
	// Decide computes the prefix decision for a request given the candidate
	// replicas (the selector-filtered set). It does not consult load - load
	// filtering happens in the DB transaction.
	Decide(model string, chain []uint64, candidates []ReplicaKey, now time.Time) PrefixDecision
	// Observe records that the replica served the request whose prefix is chain.
	// Returns true when the assignment was new or extended (caller broadcasts).
	Observe(model string, chain []uint64, key ReplicaKey, now time.Time) bool
	// Invalidate drops all entries for ONE replica.
	Invalidate(model string, key ReplicaKey)
	// InvalidateNode drops entries for ALL replicas of a node.
	InvalidateNode(model, nodeID string)
	// Evict sweeps expired entries for all models.
	Evict(now time.Time)
}
