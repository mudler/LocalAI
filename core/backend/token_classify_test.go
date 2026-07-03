package backend

import (
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
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

var _ = Describe("tokenClassifyTrace", func() {
	cfg := config.ModelConfig{Name: "privacy-filter", Backend: "privacy-filter"}
	ents := []TokenEntity{{Group: "SSN", Start: 5, End: 16, Score: 0.62, Text: "123-45-6789"}}

	It("captures model, input preview, threshold and per-entity detail", func() {
		tr := tokenClassifyTrace(cfg, "ssn is 123-45-6789", 0.5, ents, time.Now(), nil)
		Expect(tr.Type).To(Equal(trace.BackendTraceTokenClassify))
		Expect(tr.ModelName).To(Equal("privacy-filter"))
		Expect(tr.Backend).To(Equal("privacy-filter"))
		Expect(tr.Summary).To(ContainSubstring("ssn is"))
		Expect(tr.Error).To(BeEmpty())
		Expect(tr.Data["input_chars"]).To(Equal(len("ssn is 123-45-6789")))
		Expect(tr.Data["threshold"]).To(BeEquivalentTo(float32(0.5)))
		Expect(tr.Data["entities"]).To(Equal(ents))
	})

	It("records the backend error string when the call failed", func() {
		tr := tokenClassifyTrace(cfg, "x", 0, nil, time.Now(), errors.New("boom"))
		Expect(tr.Error).To(Equal("boom"))
	})
})
