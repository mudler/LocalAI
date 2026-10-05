// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"errors"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
)

type usageAnimationBackend struct {
	modalityBackend
	metadata []byte
	err      error
}

func (b *usageAnimationBackend) Animate3DWithMetadata(*pb.Animate3DRequest) ([]byte, error) {
	return b.metadata, b.err
}

var _ = Describe("animation usage transport", func() {
	It("preserves usage across protobuf serialization without calling legacy inference", func() {
		backend := &usageAnimationBackend{metadata: []byte(`{"usage":{"input_units":32,"output_units":15000},"custom":{"value":true}}`)}
		result, err := (&server{llm: backend}).Animate3D(context.Background(), &pb.Animate3DRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(backend.served).To(BeZero())
		data, err := proto.Marshal(result)
		Expect(err).NotTo(HaveOccurred())
		decoded := &pb.Result{}
		Expect(proto.Unmarshal(data, decoded)).To(Succeed())
		Expect(decoded.Success).To(BeTrue())
		Expect(decoded.Metadata).To(Equal(backend.metadata))
	})
	It("propagates failures without usage", func() {
		backend := &usageAnimationBackend{err: errors.New("generation failed")}
		result, err := (&server{llm: backend}).Animate3D(context.Background(), &pb.Animate3DRequest{})
		Expect(err).To(MatchError("generation failed"))
		Expect(result).To(BeNil())
	})
	It("supports backends without usage reporting", func() {
		backend := &modalityBackend{}
		result, err := (&server{llm: backend}).Animate3D(context.Background(), &pb.Animate3DRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Success).To(BeTrue())
		Expect(result.Metadata).To(BeEmpty())
		Expect(backend.served).To(Equal(1))
	})
})
