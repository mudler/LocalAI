// Package clusterrouting holds the transport-agnostic replica selection policy
// shared by the NATS distributed mode (core/services/nodes) and the p2p
// federation server (core/p2p). It deliberately depends on nothing heavier than
// the standard library so either transport can import it without pulling in a
// database driver or message bus.
package clusterrouting

import "time"

// ReplicaCandidate is the minimum view of a loaded model replica needed to
// apply the routing policy. It is intentionally decoupled from any storage
// model (gorm rows on the NATS side, gossiped NodeData on the p2p side) so the
// same picker runs against fresh DB rows, an in-memory snapshot, or p2p gossip.
type ReplicaCandidate struct {
	NodeID        string
	Address       string
	ReplicaIndex  int
	InFlight      int
	LastUsed      time.Time
	AvailableVRAM uint64
}

// PickBestReplica is the single source of truth for which loaded replica of a
// model serves the next request.
//
// Policy (ordered tiers, first non-tie wins):
//  1. Least in-flight wins: primary load-balancing signal.
//  2. Oldest last_used wins: round-robin between equally-loaded replicas.
//     Every successful pick refreshes last_used (in the NATS
//     FindAndLockNodeWithModel transaction and in TouchNodeModel on cache
//     hits), so the "oldest" tier naturally rotates through the candidate set
//     without a separate cursor.
//  3. Largest available_vram wins: cold-start tiebreaker for replicas that
//     have never been picked (identical last_used).
//
// The NATS SQL ORDER BY in FindAndLockNodeWithModel (registry.go) MUST mirror
// this function; registry_test.go's "agrees with PickBestReplica on a seeded
// dataset (policy mirror)" spec asserts both sides agree on a representative
// dataset and fails fast if they drift.
//
// Returns nil when the candidate list is empty. Does not allocate.
func PickBestReplica(candidates []ReplicaCandidate) *ReplicaCandidate {
	if len(candidates) == 0 {
		return nil
	}
	best := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		c := &candidates[i]
		if betterReplica(c, best) {
			best = c
		}
	}
	return best
}

// betterReplica reports whether candidate a is preferred over candidate b
// under the policy documented on PickBestReplica.
func betterReplica(a, b *ReplicaCandidate) bool {
	if a.InFlight != b.InFlight {
		return a.InFlight < b.InFlight
	}
	if !a.LastUsed.Equal(b.LastUsed) {
		return a.LastUsed.Before(b.LastUsed)
	}
	return a.AvailableVRAM > b.AvailableVRAM
}
