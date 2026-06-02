package backend

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("tokenClassifyResponseToEntities", func() {
	It("returns nil for a nil response", func() {
		Expect(tokenClassifyResponseToEntities(nil)).To(BeNil())
	})

	It("maps proto entities to TokenEntity, skipping nil rows", func() {
		resp := &pb.TokenClassifyResponse{
			Entities: []*pb.TokenClassifyEntity{
				{EntityGroup: "private_person", Start: 3, End: 8, Score: 0.97, Text: "Alice"},
				nil,
				{EntityGroup: "EMAIL", Start: 20, End: 40, Score: 0.5, Text: "a@b.com"},
			},
		}
		Expect(tokenClassifyResponseToEntities(resp)).To(Equal([]TokenEntity{
			{Group: "private_person", Start: 3, End: 8, Score: 0.97, Text: "Alice"},
			{Group: "EMAIL", Start: 20, End: 40, Score: 0.5, Text: "a@b.com"},
		}))
	})

	It("returns an empty (non-nil) slice for a response with no entities", func() {
		out := tokenClassifyResponseToEntities(&pb.TokenClassifyResponse{})
		Expect(out).NotTo(BeNil())
		Expect(out).To(BeEmpty())
	})
})
