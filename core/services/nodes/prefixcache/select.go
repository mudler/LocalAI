package prefixcache

// Candidate is a load-eligible-or-not replica view from the registry.
type Candidate struct {
	NodeID   string
	InFlight int
}

// PrefixDecision is computed from the in-memory tree before the DB transaction.
// HotNodeID is the node holding the longest prefix match (or ""); MatchRatio is
// matched/total for that match. ColdOrder lists candidate node IDs ascending by
// cacheWeight (lowest = least valuable warm cache = best cold target).
type PrefixDecision struct {
	HotNodeID  string
	MatchRatio float64
	ColdOrder  []string
}

// Select implements filter-then-score: keep candidates within the load guard,
// then prefer the hot-match node, else the lowest-cacheWeight eligible node.
// Returns "" when nothing is selectable (caller falls back to default order).
func Select(cands []Candidate, d PrefixDecision, cfg Config) string {
	if len(cands) == 0 {
		return ""
	}
	minIF := cands[0].InFlight
	for _, c := range cands {
		minIF = min(minIF, c.InFlight)
	}
	eligible := map[string]bool{}
	for _, c := range cands {
		withinAbs := c.InFlight <= minIF+cfg.BalanceAbsThreshold
		// +1 softens the relative guard when minIF==0 so a zero baseline does
		// not require exact-zero in-flight; the absolute guard governs near 0.
		withinRel := float64(c.InFlight) <= float64(minIF)*cfg.BalanceRelThreshold+1
		if withinAbs && withinRel {
			eligible[c.NodeID] = true
		}
	}
	// Hot match wins if eligible and strong enough.
	if d.HotNodeID != "" && d.MatchRatio >= cfg.MinPrefixMatch && eligible[d.HotNodeID] {
		return d.HotNodeID
	}
	// Cold placement: lowest cacheWeight eligible node.
	for _, id := range d.ColdOrder {
		if eligible[id] {
			return id
		}
	}
	// No cold ranking covered the eligible set: pick any eligible node
	// deterministically (least in-flight, then node id) so behavior is stable.
	best := ""
	bestIF := 0
	for _, c := range cands {
		if !eligible[c.NodeID] {
			continue
		}
		if best == "" || c.InFlight < bestIF || (c.InFlight == bestIF && c.NodeID < best) {
			best, bestIF = c.NodeID, c.InFlight
		}
	}
	return best
}
