package nodes

import "github.com/mudler/LocalAI/pkg/clusterrouting"

// ReplicaCandidate aliases the canonical type in pkg/clusterrouting. The policy
// implementation moved there so the p2p federation server can share it without
// importing this package (which pulls in gorm). Because this is a type alias,
// existing references such as the LoadedReplicaStats interface method and the
// ReplicaCandidate(rw) row conversion in registry.go remain valid unchanged.
type ReplicaCandidate = clusterrouting.ReplicaCandidate

// PickBestReplica delegates to the canonical implementation in pkg/clusterrouting.
// The SQL ORDER BY in FindAndLockNodeWithModel (registry.go) must mirror it; the
// "policy mirror" spec in registry_test.go asserts they agree.
func PickBestReplica(candidates []ReplicaCandidate) *ReplicaCandidate {
	return clusterrouting.PickBestReplica(candidates)
}
