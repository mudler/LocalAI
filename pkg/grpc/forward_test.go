package grpc

import (
	"context"
	"errors"
	"io"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// echoForwardModel is a minimal AIModel that just echoes Forward
// requests back as replies — used to exercise the in-process bidi
// plumbing without standing up a real HTTP upstream.
type echoForwardModel struct {
	base.SingleThread
}

func (m *echoForwardModel) Forward(_ context.Context, in <-chan *pb.ForwardRequest, out chan<- *pb.ForwardReply) error {
	defer close(out)
	first := true
	for req := range in {
		if first {
			out <- &pb.ForwardReply{
				Status:  200,
				Headers: []*pb.ForwardHeader{{Name: "Content-Type", Value: "text/event-stream"}},
			}
			first = false
		}
		out <- &pb.ForwardReply{BodyChunk: req.BodyChunk}
	}
	return nil
}

var _ = Describe("Forward RPC (in-process)", func() {
	It("round-trips status, headers, and body chunks", func() {
		// Provide registers an AIModel under a virtual address so
		// NewClient routes via the in-process embedBackend instead of
		// dialing a real socket.
		addr := "test://forward-echo"
		Provide(addr, &echoForwardModel{})
		c := NewClient(addr, true, nil, false)

		stream, err := c.Forward(context.Background())
		Expect(err).NotTo(HaveOccurred())

		// One initial request carrying path/method/headers, then two body chunks.
		Expect(stream.Send(&pb.ForwardRequest{
			Path:      "/v1/chat/completions",
			Method:    "POST",
			Headers:   []*pb.ForwardHeader{{Name: "Authorization", Value: "Bearer x"}},
			BodyChunk: []byte(`{"hello":`),
		})).To(Succeed())
		Expect(stream.Send(&pb.ForwardRequest{BodyChunk: []byte(`"world"}`)})).To(Succeed())
		Expect(stream.CloseSend()).To(Succeed())

		// First reply carries status + headers.
		first, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Status).To(Equal(int32(200)))
		Expect(first.Headers).To(HaveLen(1))
		Expect(first.Headers[0].Name).To(Equal("Content-Type"))

		// Body echoes back, one reply per request chunk.
		var body []byte
		for {
			r, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			body = append(body, r.BodyChunk...)
		}
		Expect(string(body)).To(Equal(`{"hello":"world"}`))
	})

	It("UnimplementedBase returns an error on Forward", func() {
		// The default base.Base.Forward returns "unimplemented" — any
		// backend that doesn't opt in should surface that to callers
		// rather than silently succeed.
		addr := "test://forward-base"
		Provide(addr, &base.SingleThread{})
		c := NewClient(addr, true, nil, false)

		stream, err := c.Forward(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.CloseSend()).To(Succeed())

		_, err = stream.Recv()
		Expect(err).To(HaveOccurred())
	})
})
