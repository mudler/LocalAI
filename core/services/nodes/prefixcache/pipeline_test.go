package prefixcache_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

type filterFunc func([]prefixcache.Candidate) []prefixcache.Candidate

func (f filterFunc) Filter(c []prefixcache.Candidate) []prefixcache.Candidate { return f(c) }

type scorerFunc func(prefixcache.Candidate) (float64, bool)

func (f scorerFunc) Score(c prefixcache.Candidate) (float64, bool) { return f(c) }

type pickerFunc func([]prefixcache.ScoredCandidate) (prefixcache.ReplicaKey, bool)

func (f pickerFunc) Pick(c []prefixcache.ScoredCandidate) (prefixcache.ReplicaKey, bool) {
	return f(c)
}

var _ = Describe("RoutingPipeline", func() {
	cand := func(node string, replica, inflight int) prefixcache.Candidate {
		return prefixcache.Candidate{Key: rk(node, replica), InFlight: inflight}
	}

	It("filters ineligible replicas before scoring", func() {
		pipeline := prefixcache.RoutingPipeline{
			Filters: []prefixcache.CandidateFilter{filterFunc(func(c []prefixcache.Candidate) []prefixcache.Candidate {
				return c[1:]
			})},
			Scorers: []prefixcache.WeightedScorer{{
				Weight: 1,
				Scorer: scorerFunc(func(c prefixcache.Candidate) (float64, bool) {
					if c.Key.NodeID == "A" {
						return 1, true
					}
					return 0.25, true
				}),
			}},
		}

		got, ok := pipeline.Pick([]prefixcache.Candidate{cand("A", 0, 0), cand("B", 0, 0)})
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0)))
	})

	It("combines normalized scorer results using configured weights", func() {
		pipeline := prefixcache.RoutingPipeline{
			Scorers: []prefixcache.WeightedScorer{
				{Weight: 1, Scorer: scorerFunc(func(c prefixcache.Candidate) (float64, bool) {
					if c.Key.NodeID == "A" {
						return 1, true
					}
					return 0, true
				})},
				{Weight: 3, Scorer: scorerFunc(func(c prefixcache.Candidate) (float64, bool) {
					if c.Key.NodeID == "B" {
						return 1, true
					}
					return 0, true
				})},
			},
		}

		got, ok := pipeline.Pick([]prefixcache.Candidate{cand("A", 0, 0), cand("B", 0, 0)})
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0)))
	})

	It("ignores scorers that cannot score a candidate", func() {
		pipeline := prefixcache.RoutingPipeline{
			Scorers: []prefixcache.WeightedScorer{
				{Weight: 100, Scorer: scorerFunc(func(prefixcache.Candidate) (float64, bool) {
					return 1, false
				})},
				{Weight: 1, Scorer: scorerFunc(func(c prefixcache.Candidate) (float64, bool) {
					if c.Key.NodeID == "B" {
						return 0.75, true
					}
					return 0.25, true
				})},
			},
		}

		got, ok := pipeline.Pick([]prefixcache.Candidate{cand("A", 0, 1), cand("B", 0, 2)})
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0)))
	})

	It("uses a weighted sum when signal availability differs by candidate", func() {
		pipeline := prefixcache.RoutingPipeline{
			Scorers: []prefixcache.WeightedScorer{
				{Weight: 1, Scorer: scorerFunc(func(c prefixcache.Candidate) (float64, bool) {
					return 0.6, c.Key.NodeID == "A"
				})},
				{Weight: 1, Scorer: scorerFunc(func(c prefixcache.Candidate) (float64, bool) {
					return 0.5, c.Key.NodeID == "B"
				})},
				{Weight: 1, Scorer: scorerFunc(func(c prefixcache.Candidate) (float64, bool) {
					return 0.5, c.Key.NodeID == "B"
				})},
			},
		}

		got, ok := pipeline.Pick([]prefixcache.Candidate{cand("A", 0, 0), cand("B", 0, 0)})
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0)))
	})

	It("uses replica key ordering as a deterministic score tie-break", func() {
		pipeline := prefixcache.RoutingPipeline{}
		got, ok := pipeline.Pick([]prefixcache.Candidate{
			cand("B", 1, 0), cand("A", 1, 0), cand("A", 0, 0),
		})
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("A", 0)))
	})

	It("delegates final selection to the configured picker", func() {
		pipeline := prefixcache.RoutingPipeline{
			Picker: pickerFunc(func(candidates []prefixcache.ScoredCandidate) (prefixcache.ReplicaKey, bool) {
				return candidates[len(candidates)-1].Candidate.Key, true
			}),
		}
		got, ok := pipeline.Pick([]prefixcache.Candidate{cand("A", 0, 0), cand("B", 0, 0)})
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0)))
	})
})

var _ = Describe("ValidateScorerWeights", func() {
	It("accepts the named prefix-cache scorer", func() {
		Expect(prefixcache.ValidateScorerWeights(map[string]float64{
			prefixcache.ScorerPrefixCache: 0.5,
		})).To(Succeed())
	})

	It("rejects negative and unknown scorer weights", func() {
		Expect(prefixcache.ValidateScorerWeights(map[string]float64{
			prefixcache.ScorerPrefixCache: -1,
		})).To(MatchError(ContainSubstring("must be >= 0")))
		Expect(prefixcache.ValidateScorerWeights(map[string]float64{
			"latency": 1,
		})).To(MatchError(ContainSubstring("unknown routing scorer")))
	})
})
