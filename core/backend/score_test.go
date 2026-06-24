package backend

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("scoreResponseToCandidates", func() {
	It("returns nil for a nil response", func() {
		Expect(scoreResponseToCandidates(nil, false)).To(BeNil())
	})

	It("returns an empty slice when the response has no candidates", func() {
		Expect(scoreResponseToCandidates(&pb.ScoreResponse{}, false)).To(BeEmpty())
	})

	It("copies LogProb / LengthNormalizedLogProb / NumTokens for every candidate", func() {
		resp := &pb.ScoreResponse{Candidates: []*pb.CandidateScore{
			{LogProb: -2.0, LengthNormalizedLogProb: -1.0, NumTokens: 2},
			{LogProb: -7.5, LengthNormalizedLogProb: -1.5, NumTokens: 5},
		}}
		got := scoreResponseToCandidates(resp, false)
		Expect(got).To(HaveLen(2))
		Expect(got[0].LogProb).To(Equal(-2.0))
		Expect(got[0].LengthNormalizedLogProb).To(Equal(-1.0))
		Expect(got[0].NumTokens).To(Equal(2))
		Expect(got[1].LogProb).To(Equal(-7.5))
		Expect(got[1].NumTokens).To(Equal(5))
	})

	It("omits per-token detail when includeTokens=false even if the wire response carries it", func() {
		// Defensive: if the backend over-reports we still respect the
		// caller's opt-in so consumers don't pay marshaling for data
		// they didn't ask for.
		resp := &pb.ScoreResponse{Candidates: []*pb.CandidateScore{{
			LogProb: -1.0,
			Tokens:  []*pb.TokenLogProb{{Token: "hi", LogProb: -1.0}},
		}}}
		got := scoreResponseToCandidates(resp, false)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Tokens).To(BeNil())
	})

	It("populates per-token detail when includeTokens=true", func() {
		resp := &pb.ScoreResponse{Candidates: []*pb.CandidateScore{{
			LogProb:   -3.0,
			NumTokens: 2,
			Tokens: []*pb.TokenLogProb{
				{Token: "Hello", LogProb: -1.0},
				{Token: " world", LogProb: -2.0},
			},
		}}}
		got := scoreResponseToCandidates(resp, true)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Tokens).To(HaveLen(2))
		Expect(got[0].Tokens[0].Token).To(Equal("Hello"))
		Expect(got[0].Tokens[0].LogProb).To(Equal(-1.0))
		Expect(got[0].Tokens[1].Token).To(Equal(" world"))
		Expect(got[0].Tokens[1].LogProb).To(Equal(-2.0))
	})
})
