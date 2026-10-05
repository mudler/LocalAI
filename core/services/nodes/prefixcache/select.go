package prefixcache

import "sort"

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
	pipeline := RoutingPipeline{
		Filters: []CandidateFilter{loadGuardFilter{cfg: cfg}},
		Scorers: []WeightedScorer{{
			Weight: scorerWeight(cfg.ScorerWeights, ScorerPrefixCache, 1),
			Scorer: newPrefixPreferenceScorer(cands, d, cfg),
		}},
	}
	return pipeline.Pick(cands)
}

func scorerWeight(weights map[string]float64, name string, fallback float64) float64 {
	if weight, ok := weights[name]; ok {
		return weight
	}
	return fallback
}

type loadGuardFilter struct {
	cfg Config
}

func (f loadGuardFilter) Filter(candidates []Candidate) []Candidate {
	if len(candidates) == 0 {
		return nil
	}
	minInFlight := candidates[0].InFlight
	for _, candidate := range candidates[1:] {
		minInFlight = min(minInFlight, candidate.InFlight)
	}
	filtered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		withinAbs := candidate.InFlight <= minInFlight+f.cfg.BalanceAbsThreshold
		// +1 softens the relative guard when minInFlight is zero; the absolute
		// guard remains the controlling signal near zero.
		withinRel := float64(candidate.InFlight) <= float64(minInFlight)*f.cfg.BalanceRelThreshold+1
		if withinAbs && withinRel {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

type prefixPreferenceScorer struct {
	scores map[ReplicaKey]float64
}

func newPrefixPreferenceScorer(candidates []Candidate, decision PrefixDecision, cfg Config) prefixPreferenceScorer {
	scores := make(map[ReplicaKey]float64, len(candidates))

	// The fallback occupies the bottom quarter of the normalized range. It
	// preserves the previous least-in-flight, then replica-key ordering.
	fallback := append([]Candidate(nil), candidates...)
	sort.Slice(fallback, func(i, j int) bool {
		if fallback[i].InFlight != fallback[j].InFlight {
			return fallback[i].InFlight < fallback[j].InFlight
		}
		return fallback[i].Key.less(fallback[j].Key)
	})
	for i, candidate := range fallback {
		scores[candidate.Key] = 0.25 * float64(len(fallback)-i) / float64(len(fallback)+1)
	}

	// Cold placement occupies the middle half, in cache-weight order.
	for i, key := range decision.ColdOrder {
		scores[key] = 0.75 - 0.25*float64(i)/float64(len(decision.ColdOrder)+1)
	}

	// A sufficiently strong hot match remains the highest-scoring signal.
	if decision.HasHot && decision.MatchRatio >= cfg.MinPrefixMatch {
		scores[decision.Hot] = 1
	}
	return prefixPreferenceScorer{scores: scores}
}

func (s prefixPreferenceScorer) Score(candidate Candidate) (float64, bool) {
	score, ok := s.scores[candidate.Key]
	return score, ok
}
