// SPDX-License-Identifier: MIT
package model

import (
	"context"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type routingMetadataBackend struct {
	grpcpkg.Backend
	err error
}

func (b *routingMetadataBackend) ModelMetadata(context.Context, *pb.ModelOptions, ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return nil, b.err
}

var _ = Describe("Multimodal routing", func() {
	It("local metadata connection failure evicts exactly once", func() {
		calls := 0
		b := newConnectionEvictingClient(&routingMetadataBackend{err: status.Error(codes.Unavailable, "connection refused")}, "same", func() { calls++ })
		for i := 0; i < 2; i++ {
			_, err := b.ModelMetadata(context.Background(), &pb.ModelOptions{})
			Expect(err).To(HaveOccurred())
		}
		Expect(calls).To(Equal(1))
	})
	It("local worker-owned metadata refusal does not evict", func() {
		calls := 0
		b := newConnectionEvictingClient(&routingMetadataBackend{err: status.Error(codes.Unimplemented, "unsupported")}, "same", func() { calls++ })
		_, err := b.ModelMetadata(context.Background(), &pb.ModelOptions{})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(calls).To(BeZero())
	})
})
