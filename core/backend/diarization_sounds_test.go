// SPDX-License-Identifier: MIT
package backend

import (
	"encoding/json"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("diarization sound events", func() {
	It("sends include_sounds to the backend only when asked", func() {
		Expect((&DiarizationRequest{IncludeSounds: true}).toProto(1, "m").IncludeSounds).To(BeTrue())
		Expect((&DiarizationRequest{}).toProto(1, "m").IncludeSounds).To(BeFalse())
	})

	It("maps proto sounds to the result and keeps an empty list when the backend heard nothing", func() {
		out := diarizationResultFromProto(&pb.DiarizeResponse{
			SoundsIncluded: true,
			Sounds:         []*pb.DiarizeSound{{Start: 1, End: 2.5, Label: "Cough", Confidence: 0.5}},
		})
		Expect(out.Sounds).To(HaveLen(1))
		Expect(out.Sounds[0].Label).To(Equal("Cough"))
		Expect(out.Sounds[0].Start).To(BeNumerically("~", 1, 1e-6))
		Expect(out.Sounds[0].End).To(BeNumerically("~", 2.5, 1e-6))
		Expect(out.Sounds[0].Confidence).To(BeNumerically("~", 0.5, 1e-6))

		empty := diarizationResultFromProto(&pb.DiarizeResponse{SoundsIncluded: true})
		raw, err := json.Marshal(empty)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"sounds":[]`))
	})

	It("leaves sounds out of the payload when they were not requested", func() {
		raw, err := json.Marshal(diarizationResultFromProto(&pb.DiarizeResponse{}))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).ToNot(ContainSubstring("sounds"))
	})
})
