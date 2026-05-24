package nodes

import "time"

// ReplicaCandidate is the minimum view of a loaded model replica needed to
// apply the routing policy. It is intentionally decoupled from the gorm models
// (BackendNode, NodeModel) so the same picker can run against fresh DB rows
// (SmartRouter.Route → FindAndLockNodeWithModel) and against an in-memory
// snapshot (the per-frontend rotating cache flagged in pkg/model — see TODO
// below).
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
//  1. Least in-flight wins — primary load-balancing signal.
//  2. Oldest last_used wins — round-robin between equally-loaded replicas.
//     Every successful pick refreshes last_used (in FindAndLockNodeWithModel's
//     transaction and in TouchNodeModel on cache hits), so the "oldest" tier
//     naturally rotates through the candidate set without a separate cursor.
//  3. Largest available_vram wins — cold-start tiebreaker for replicas that
//     have never been picked (identical last_used).
//
// Two callers must agree on this policy:
//
//   - SmartRouter.Route, via the SQL ORDER BY in FindAndLockNodeWithModel
//     (registry.go). That query MUST mirror this function — TestPickerSQLMirror
//     asserts both sides agree on a representative dataset.
//
//   - The per-frontend rotating-replica cache (NOT YET IMPLEMENTED — see
//     pkg/model/loader.go and pkg/model/initializers.go for the integration
//     point). When that cache lands, it will call PickBestReplica against an
//     in-memory snapshot using locally-tracked in-flight counters and skip the
//     per-request DB round-trip.
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
