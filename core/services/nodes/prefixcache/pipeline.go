package prefixcache

import "fmt"

const ScorerPrefixCache = "prefix_cache"

func ValidateScorerWeights(weights map[string]float64) error {
	for name, weight := range weights {
		if name != ScorerPrefixCache {
			return fmt.Errorf("prefixcache: unknown routing scorer %q", name)
		}
		if weight < 0 {
			return fmt.Errorf("prefixcache: scorer weight for %q must be >= 0", name)
		}
	}
	return nil
}

// CandidateFilter removes replicas that are not eligible for a routing
// decision. Filters run in declaration order.
type CandidateFilter interface {
	Filter([]Candidate) []Candidate
}

// CandidateScorer returns a normalized score in [0,1]. scored=false means the
// scorer has no signal for this candidate and its weight is excluded.
type CandidateScorer interface {
	Score(Candidate) (score float64, scored bool)
}

// WeightedScorer configures one independent routing signal.
type WeightedScorer struct {
	Scorer CandidateScorer
	Weight float64
}

// ScoredCandidate is the normalized pipeline output passed to a picker.
type ScoredCandidate struct {
	Candidate Candidate
	Score     float64
}

// CandidatePicker owns the final selection policy.
type CandidatePicker interface {
	Pick([]ScoredCandidate) (ReplicaKey, bool)
}

// RoutingPipeline composes eligibility filters and independent scorers. The
// highest weighted mean wins; ReplicaKey ordering makes ties deterministic.
type RoutingPipeline struct {
	Filters []CandidateFilter
	Scorers []WeightedScorer
	Picker  CandidatePicker
}

func (p RoutingPipeline) Pick(candidates []Candidate) (ReplicaKey, bool) {
	filtered := append([]Candidate(nil), candidates...)
	for _, filter := range p.Filters {
		filtered = filter.Filter(filtered)
	}
	if len(filtered) == 0 {
		return ReplicaKey{}, false
	}

	scored := make([]ScoredCandidate, len(filtered))
	for i, candidate := range filtered {
		scored[i] = ScoredCandidate{Candidate: candidate, Score: p.score(candidate)}
	}
	if p.Picker != nil {
		return p.Picker.Pick(scored)
	}
	return HighestScorePicker{}.Pick(scored)
}

func (p RoutingPipeline) score(candidate Candidate) float64 {
	var sum float64
	for _, weighted := range p.Scorers {
		if weighted.Scorer == nil || weighted.Weight <= 0 {
			continue
		}
		score, ok := weighted.Scorer.Score(candidate)
		if !ok {
			continue
		}
		score = max(0, min(1, score))
		sum += score * weighted.Weight
	}
	return sum
}

// HighestScorePicker picks the maximum score and breaks ties by replica key.
type HighestScorePicker struct{}

func (HighestScorePicker) Pick(candidates []ScoredCandidate) (ReplicaKey, bool) {
	if len(candidates) == 0 {
		return ReplicaKey{}, false
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Score > best.Score ||
			(candidate.Score == best.Score && candidate.Candidate.Key.less(best.Candidate.Key)) {
			best = candidate
		}
	}
	return best.Candidate.Key, true
}
