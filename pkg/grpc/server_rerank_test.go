package grpc

import (
	"context"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// rerankBackend implements RerankModel on top of the minimal AIModel surface,
// mirroring how a Go backend would opt into Score today.
type rerankBackend struct {
	base.SingleThread
}

func (b *rerankBackend) Rerank(context.Context, *pb.RerankRequest) (*pb.RerankResult, error) {
	return &pb.RerankResult{
		Results: []*pb.DocumentResult{{Index: 0, RelevanceScore: 1}},
	}, nil
}

var _ AIModel = (*rerankBackend)(nil)
var _ RerankModel = (*rerankBackend)(nil)

var _ = Describe("Rerank", func() {
	It("is served when the backend implements RerankModel", func() {
		Provide("test://rerank-served", &rerankBackend{})
		c := NewClient("test://rerank-served", true, nil, false)

		res, err := c.Rerank(context.Background(), &pb.RerankRequest{Query: "q", Documents: []string{"a", "b"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Results).To(HaveLen(1))
	})

	It("reports Unimplemented when the backend does not implement RerankModel", func() {
		Provide("test://rerank-unimplemented", &base.SingleThread{})
		c := NewClient("test://rerank-unimplemented", true, nil, false)

		_, err := c.Rerank(context.Background(), &pb.RerankRequest{Query: "q"})
		Expect(err).To(HaveOccurred())
		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unimplemented))
	})
})
