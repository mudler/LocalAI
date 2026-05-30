package prefixcache

// ReplicaKey identifies a specific loaded replica (a backend process). Affinity
// is tracked per replica, not per node, because each replica is a separate
// process with its own KV cache.
type ReplicaKey struct {
	NodeID  string
	Replica int
}

// less reports whether a sorts before b, ordering by NodeID then Replica. It is
// the deterministic tiebreak used wherever two replicas are otherwise equal.
func (a ReplicaKey) less(b ReplicaKey) bool {
	if a.NodeID != b.NodeID {
		return a.NodeID < b.NodeID
	}
	return a.Replica < b.Replica
}

// Candidate is a load-eligible-or-not replica view from the registry. There is
// one Candidate per LOADED replica: the router no longer collapses replicas per
// node, so two replicas of the same model on the same node are two candidates.
type Candidate struct {
	Key      ReplicaKey
	InFlight int
}

// PrefixDecision is computed from the in-memory tree before the DB transaction.
// Hot is the replica holding the longest prefix match and HasHot reports whether
// there is one (a ReplicaKey has no "" sentinel). MatchRatio is matched/total
// for that match. ColdOrder lists candidate replicas ascending by cacheWeight
// (lowest = least valuable warm cache = best cold target).
type PrefixDecision struct {
	Hot        ReplicaKey
	HasHot     bool
	MatchRatio float64
	ColdOrder  []ReplicaKey
}

// Select implements filter-then-score per replica: keep candidates within the
// load guard (relative to the min in-flight across ALL candidate replicas), then
// prefer the exact hot-match replica, else the lowest-cacheWeight eligible
// replica via ColdOrder, else a deterministic eligible fallback (least in-flight,
// tiebreak by NodeID then Replica). Returns (ReplicaKey{}, false) when nothing is
// selectable.
func Select(cands []Candidate, d PrefixDecision, cfg Config) (ReplicaKey, bool) {
	if len(cands) == 0 {
		return ReplicaKey{}, false
	}
	minIF := cands[0].InFlight
	for _, c := range cands {
		minIF = min(minIF, c.InFlight)
	}
	eligible := map[ReplicaKey]bool{}
	for _, c := range cands {
		withinAbs := c.InFlight <= minIF+cfg.BalanceAbsThreshold
		// +1 softens the relative guard when minIF==0 so a zero baseline does
		// not require exact-zero in-flight; the absolute guard governs near 0.
		withinRel := float64(c.InFlight) <= float64(minIF)*cfg.BalanceRelThreshold+1
		if withinAbs && withinRel {
			eligible[c.Key] = true
		}
	}
	// Hot match wins if eligible and strong enough.
	if d.HasHot && d.MatchRatio >= cfg.MinPrefixMatch && eligible[d.Hot] {
		return d.Hot, true
	}
	// Cold placement: lowest cacheWeight eligible replica.
	for _, k := range d.ColdOrder {
		if eligible[k] {
			return k, true
		}
	}
	// Deterministic eligible fallback: least in-flight, tiebreak NodeID then
	// Replica. ColdOrder may not cover the eligible set (the caller may pass an
	// empty ColdOrder), so this guarantees Select still returns the best eligible
	// replica rather than failing.
	var best Candidate
	found := false
	for _, c := range cands {
		if !eligible[c.Key] {
			continue
		}
		if !found || c.InFlight < best.InFlight ||
			(c.InFlight == best.InFlight && c.Key.less(best.Key)) {
			best, found = c, true
		}
	}
	if found {
		return best.Key, true
	}
	return ReplicaKey{}, false
}
