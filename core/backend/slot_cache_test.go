package backend

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeSlotCache struct {
	got  *pb.SlotCacheRequest
	resp *pb.SlotCacheResponse
	err  error
}

func (f *fakeSlotCache) SlotCache(_ context.Context, in *pb.SlotCacheRequest, _ ...grpc.CallOption) (*pb.SlotCacheResponse, error) {
	f.got = in
	return f.resp, f.err
}

var _ = Describe("slot cache backend call", func() {
	It("maps UNIMPLEMENTED to ErrSlotCacheUnsupported", func() {
		f := &fakeSlotCache{err: status.Error(codes.Unimplemented, "method SlotCache not implemented")}
		_, err := callSlotCache(context.Background(), f, &pb.SlotCacheRequest{})
		Expect(errors.Is(err, ErrSlotCacheUnsupported)).To(BeTrue())
	})

	It("leaves other gRPC errors unchanged", func() {
		orig := status.Error(codes.FailedPrecondition, "slot save/restore is disabled")
		_, err := callSlotCache(context.Background(), &fakeSlotCache{err: orig}, &pb.SlotCacheRequest{})
		Expect(err).To(Equal(orig))
		Expect(errors.Is(err, ErrSlotCacheUnsupported)).To(BeFalse())
	})

	It("leaves non-gRPC errors unchanged", func() {
		orig := errors.New("dial failed")
		_, err := callSlotCache(context.Background(), &fakeSlotCache{err: orig}, &pb.SlotCacheRequest{})
		Expect(err).To(Equal(orig))
	})

	It("returns the response on success", func() {
		want := &pb.SlotCacheResponse{Success: true, NTokens: 7}
		got, err := callSlotCache(context.Background(), &fakeSlotCache{resp: want}, &pb.SlotCacheRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(want))
	})

	It("returns the issued ID and the message", func() {
		f := &fakeSlotCache{resp: &pb.SlotCacheResponse{Success: true, Id: "0123456789abcdef0123456789abcdef", Message: "pruned x", Slot: 1, NTokens: 3, NBytes: 9}}
		cfg := config.ModelConfig{Name: "m"}
		resp, err := callSlotCache(context.Background(), f, newSlotCacheRequest(pb.SlotCacheAction_SLOT_CACHE_ACTION_SAVE, 1, "", cfg))
		Expect(err).ToNot(HaveOccurred())
		Expect(f.got.GetId()).To(BeEmpty())
		Expect(resp.GetId()).To(Equal("0123456789abcdef0123456789abcdef"))
	})

	It("sends the configured model as the identity", func() {
		cfg := config.ModelConfig{Name: "alias"}
		cfg.Model = "weights.gguf"
		req := newSlotCacheRequest(pb.SlotCacheAction_SLOT_CACHE_ACTION_RESTORE, 2, "0123456789abcdef0123456789abcdef", cfg)
		f := &fakeSlotCache{resp: &pb.SlotCacheResponse{Success: true}}
		_, err := callSlotCache(context.Background(), f, req)
		Expect(err).ToNot(HaveOccurred())
		Expect(f.got.GetModelIdentity()).To(Equal("weights.gguf"))
		Expect(f.got.GetAction()).To(Equal(pb.SlotCacheAction_SLOT_CACHE_ACTION_RESTORE))
		Expect(f.got.GetSlot()).To(Equal(int32(2)))
		Expect(f.got.GetId()).To(Equal("0123456789abcdef0123456789abcdef"))
	})
})
