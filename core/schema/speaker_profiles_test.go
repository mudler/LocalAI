// SPDX-License-Identifier: MIT

package schema

import (
	"encoding/json"
	"math"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Portable speaker profiles", func() {
	var p SpeakerProfiles
	var trusted SpeakerEncoder
	BeforeEach(func() {
		trusted = SpeakerEncoder{Identity: "sha256:" + strings.Repeat("a", 64), Dimension: 2}
		p = SpeakerProfiles{Version: 1, Encoder: trusted, Speakers: []SpeakerProfile{{Speaker: 3, CleanDuration: 3, Intervals: []SpeakerProfileInterval{{Start: 0, End: 3}}, Embedding: []float32{0.6, 0.8}}}}
	})
	It("selects the requested slot against independently supplied metadata", func() {
		selected, err := p.Select(3, trusted)
		Expect(err).NotTo(HaveOccurred())
		Expect(selected).To(Equal(p.Speakers[0]))
		_, err = p.Select(0, trusted)
		Expect(err).To(HaveOccurred())
		p.Encoder.Identity = "sha256:" + strings.Repeat("b", 64)
		_, err = p.Select(3, trusted)
		Expect(err).To(HaveOccurred())
		p.Encoder = trusted
		p.Encoder.Dimension = 3
		Expect(p.Validate(trusted)).To(HaveOccurred())
	})
	It("round trips the backend JSON including unavailable profiles without embeddings", func() {
		raw := `{"version":1,"encoder":{"identity":"` + trusted.Identity + `","dimension":2},"speakers":[{"speaker":3,"clean_duration":3,"intervals":[{"start":0,"end":3}],"unavailable_reason":null,"embedding":[0.6,0.8]},{"speaker":4,"clean_duration":1,"intervals":[{"start":4,"end":5}],"unavailable_reason":"insufficient_clean_speech"}]}`
		Expect(json.Unmarshal([]byte(raw), &p)).To(Succeed())
		Expect(p.Validate(trusted)).To(Succeed())
		encoded, err := json.Marshal(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(encoded).To(MatchJSON(raw))
		_, err = p.Select(4, trusted)
		Expect(err).To(HaveOccurred())
		_, err = p.Select(3, trusted)
		Expect(err).NotTo(HaveOccurred())
	})
	It("allows empty discovery but cannot select from it", func() {
		p.Speakers = []SpeakerProfile{}
		Expect(p.Validate(trusted)).To(Succeed())
		_, err := p.Select(0, trusted)
		Expect(err).To(HaveOccurred())
	})
	It("rejects unsupported versions and invalid trusted metadata", func() {
		p.Version = 2
		Expect(p.Validate(trusted)).To(HaveOccurred())
		p.Version = 1
		for _, encoder := range []SpeakerEncoder{{}, {Identity: "sha256:trusted", Dimension: 2}, {Identity: trusted.Identity, Dimension: 0}, {Identity: strings.ToUpper(trusted.Identity), Dimension: 2}} {
			p.Encoder = encoder
			Expect(p.Validate(encoder)).To(HaveOccurred())
		}
	})
	DescribeTable("rejects invalid vectors", func(vector []float32) {
		p.Speakers[0].Embedding = vector
		_, err := p.Select(3, trusted)
		Expect(err).To(HaveOccurred())
	},
		Entry("missing", []float32(nil)), Entry("zero", []float32{0, 0}),
		Entry("wrong dimension", []float32{1}), Entry("NaN", []float32{float32(math.NaN()), 1}),
		Entry("positive infinity", []float32{float32(math.Inf(1)), 1}), Entry("negative infinity", []float32{1, float32(math.Inf(-1))}),
	)
	It("accepts finite nonzero vectors without imposing a second normalization policy", func() {
		p.Speakers[0].Embedding = []float32{math.MaxFloat32, math.SmallestNonzeroFloat32}
		Expect(p.Validate(trusted)).To(Succeed())
	})
	It("rejects duplicate or negative speaker slots", func() {
		p.Speakers = append(p.Speakers, p.Speakers[0])
		Expect(p.Validate(trusted)).To(HaveOccurred())
		p.Speakers = p.Speakers[:1]
		p.Speakers[0].Speaker = -1
		Expect(p.Validate(trusted)).To(HaveOccurred())
	})
	It("rejects inconsistent unavailable status", func() {
		reason := "embedding_failed"
		p.Speakers[0].UnavailableReason = &reason
		Expect(p.Validate(trusted)).To(HaveOccurred())
		p.Speakers[0].Embedding = nil
		Expect(p.Validate(trusted)).To(Succeed())
		reason = " "
		Expect(p.Validate(trusted)).To(HaveOccurred())
	})
	DescribeTable("rejects unreasonable duration or intervals", func(duration float64, intervals []SpeakerProfileInterval) {
		p.Speakers[0].CleanDuration = duration
		p.Speakers[0].Intervals = intervals
		Expect(p.Validate(trusted)).To(HaveOccurred())
	},
		Entry("negative", -1.0, []SpeakerProfileInterval(nil)),
		Entry("nonfinite duration", math.NaN(), []SpeakerProfileInterval(nil)),
		Entry("too long", 31.0, []SpeakerProfileInterval{{0, 31}}),
		Entry("too short for enrollment", 1.0, []SpeakerProfileInterval{{0, 1}}),
		Entry("mismatch", 3.0, []SpeakerProfileInterval{{0, 2}}),
		Entry("missing spans", 3.0, []SpeakerProfileInterval(nil)),
		Entry("negative start", 3.0, []SpeakerProfileInterval{{-1, 2}}),
		Entry("reversed", 3.0, []SpeakerProfileInterval{{3, 0}}),
		Entry("empty span", 3.0, []SpeakerProfileInterval{{0, 0}, {0, 3}}),
		Entry("overlap", 3.0, []SpeakerProfileInterval{{0, 2}, {1, 2}}),
		Entry("out of order", 3.0, []SpeakerProfileInterval{{2, 4}, {0, 1}}),
		Entry("infinite end", 3.0, []SpeakerProfileInterval{{0, math.Inf(1)}}),
		Entry("NaN start", 3.0, []SpeakerProfileInterval{{math.NaN(), 3}}),
	)
	It("accepts disjoint original spans and serialization rounding", func() {
		p.Speakers[0].Intervals = []SpeakerProfileInterval{{1, 2}, {5, 7.0001}}
		Expect(p.Validate(trusted)).To(Succeed())
	})
})
