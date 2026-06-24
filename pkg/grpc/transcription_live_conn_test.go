package grpc

import (
	"context"
	"io"
	"net"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gogrpc "google.golang.org/grpc"
)

// The embedded (test://) path never dials a *grpc.ClientConn, so it cannot
// catch connection-lifecycle bugs. This suite runs the same live-ASR contract
// over a real TCP connection: the terminal FinalResult arrives AFTER the
// client closes its send side, so the conn must be released on terminal Recv
// — releasing it inside CloseSend killed the pending Recv with "grpc: the
// client connection is closing" and lost the decode tail on every turn.
func startLiveASRServer() string {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())

	s := gogrpc.NewServer(serverOpts()...)
	pb.RegisterBackendServer(s, &server{llm: &echoLiveASRModel{}})
	go func() { _ = s.Serve(lis) }()
	DeferCleanup(s.GracefulStop)

	return lis.Addr().String()
}

var _ = Describe("AudioTranscriptionLive over a real connection", func() {
	It("delivers the post-CloseSend FinalResult, then EOF releases the conn", func() {
		c := NewClient(startLiveASRServer(), true, nil, false)

		stream, err := c.AudioTranscriptionLive(context.Background())
		Expect(err).NotTo(HaveOccurred())

		Expect(stream.Send(&pb.TranscriptLiveRequest{
			Payload: &pb.TranscriptLiveRequest_Config{Config: &pb.TranscriptLiveConfig{Language: "en"}},
		})).To(Succeed())
		ack, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(ack.GetReady()).To(BeTrue())

		Expect(stream.Send(&pb.TranscriptLiveRequest{
			Payload: &pb.TranscriptLiveRequest_Audio{Audio: &pb.TranscriptLiveAudio{Pcm: []float32{0.1, 0.2, 0.3}}},
		})).To(Succeed())
		delta, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(delta.GetDelta()).To(Equal("[3]"))

		// The decisive step: the terminal message arrives after CloseSend.
		Expect(stream.CloseSend()).To(Succeed())
		final, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred(), "FinalResult must survive CloseSend — the conn may only close on terminal Recv")
		Expect(final.GetFinalResult()).NotTo(BeNil())
		Expect(final.GetFinalResult().GetText()).To(Equal("[3]"))

		_, err = stream.Recv()
		Expect(err).To(MatchError(io.EOF))
	})
})
