package clusterrouting

// PickWithAffinity prefers the candidate whose NodeID equals preferredNodeID
// when that candidate's in-flight count is within slack of the least-loaded
// candidate; otherwise it falls back to PickBestReplica (least in-flight, then
// oldest last-used, then most free VRAM). This keeps a warm prefix-cache peer
// sticky without letting it become a hot-spot: once it is more than slack
// requests busier than the least-loaded peer, load wins. With an empty
// preferredNodeID, or a preferred node not in the set, it is exactly
// PickBestReplica. slack mirrors prefixcache's BalanceAbsThreshold.
func PickWithAffinity(candidates []ReplicaCandidate, preferredNodeID string, slack int) *ReplicaCandidate {
	if len(candidates) == 0 {
		return nil
	}
	if preferredNodeID == "" {
		return PickBestReplica(candidates)
	}
	var preferred *ReplicaCandidate
	minInFlight := candidates[0].InFlight
	for i := range candidates {
		c := &candidates[i]
		if c.InFlight < minInFlight {
			minInFlight = c.InFlight
		}
		if c.NodeID == preferredNodeID {
			preferred = c
		}
	}
	if preferred != nil && preferred.InFlight <= minInFlight+slack {
		return preferred
	}
	return PickBestReplica(candidates)
}
