package model

import (
	"context"

	grpcbackend "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type liveForwardFixture struct{ grpcbackend.Backend }

func (*liveForwardFixture) TTSLive(ctx context.Context, _ *pb.TTSRequest, text <-chan string, send func([]byte) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-text:
			if !ok {
				return nil
			}
			if err := send([]byte(chunk)); err != nil {
				return err
			}
		}
	}
}

var _ = Describe("TTSLive cache wrapper", func() {
	It("preserves the optional incremental interface", func() {
		wrapped := newConnectionEvictingClient(&liveForwardFixture{}, "fixture", func() { Fail("unexpected eviction") })
		live, ok := wrapped.(grpcbackend.TTSLiveBackend)
		Expect(ok).To(BeTrue())
		text := make(chan string, 2)
		text <- "early"
		text <- "late"
		close(text)
		var got []string
		Expect(live.TTSLive(context.Background(), &pb.TTSRequest{}, text, func(b []byte) error { got = append(got, string(b)); return nil })).To(Succeed())
		Expect(got).To(Equal([]string{"early", "late"}))
	})
})
