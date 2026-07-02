package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// echoLiveASRModel is a minimal AIModel exercising the AudioTranscriptionLive
// contract: ready ack after the config, one delta per audio frame (eou set on
// frames whose first sample is negative), final_result on close.
type echoLiveASRModel struct {
	base.SingleThread
}

func (m *echoLiveASRModel) AudioTranscriptionLive(in <-chan *pb.TranscriptLiveRequest, out chan<- *pb.TranscriptLiveResponse) error {
	defer close(out)

	first, ok := <-in
	if !ok || first.GetConfig() == nil {
		return errors.New("first message must carry a config")
	}
	out <- &pb.TranscriptLiveResponse{Ready: true}

	var full strings.Builder
	for req := range in {
		audio := req.GetAudio()
		if audio == nil {
			continue
		}
		delta := fmt.Sprintf("[%d]", len(audio.Pcm))
		full.WriteString(delta)
		out <- &pb.TranscriptLiveResponse{
			Delta: delta,
			Eou:   len(audio.Pcm) > 0 && audio.Pcm[0] < 0,
		}
	}
	out <- &pb.TranscriptLiveResponse{FinalResult: &pb.TranscriptResult{Text: full.String(), Eou: true}}
	return nil
}

var _ = Describe("AudioTranscriptionLive RPC (in-process)", func() {
	It("acks the config, streams deltas with eou flags, and finalizes on CloseSend", func() {
		addr := "test://transcription-live-echo"
		Provide(addr, &echoLiveASRModel{})
		c := NewClient(addr, true, nil, false)

		stream, err := c.AudioTranscriptionLive(context.Background())
		Expect(err).NotTo(HaveOccurred())

		Expect(stream.Send(&pb.TranscriptLiveRequest{
			Payload: &pb.TranscriptLiveRequest_Config{Config: &pb.TranscriptLiveConfig{Language: "en"}},
		})).To(Succeed())

		// The ready ack must arrive before any audio is sent — this is what
		// lets callers degrade synchronously when live ASR is unsupported.
		ack, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(ack.Ready).To(BeTrue())

		Expect(stream.Send(&pb.TranscriptLiveRequest{
			Payload: &pb.TranscriptLiveRequest_Audio{Audio: &pb.TranscriptLiveAudio{Pcm: []float32{0.1, 0.2}}},
		})).To(Succeed())
		r1, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(r1.Delta).To(Equal("[2]"))
		Expect(r1.Eou).To(BeFalse())

		Expect(stream.Send(&pb.TranscriptLiveRequest{
			Payload: &pb.TranscriptLiveRequest_Audio{Audio: &pb.TranscriptLiveAudio{Pcm: []float32{-0.5, 0.0, 0.5}}},
		})).To(Succeed())
		r2, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(r2.Delta).To(Equal("[3]"))
		Expect(r2.Eou).To(BeTrue())

		Expect(stream.CloseSend()).To(Succeed())

		final, err := stream.Recv()
		Expect(err).NotTo(HaveOccurred())
		Expect(final.FinalResult).NotTo(BeNil())
		Expect(final.FinalResult.Text).To(Equal("[2][3]"))
		Expect(final.FinalResult.Eou).To(BeTrue())

		_, err = stream.Recv()
		Expect(errors.Is(err, io.EOF)).To(BeTrue())
	})

	It("surfaces Unimplemented from base.Base on the first Recv without CloseSend", func() {
		// The ready-ack contract: a caller that sent its config and blocks on
		// the first Recv must get the unsupported signal immediately — NOT
		// after it gives up and closes the send side.
		addr := "test://transcription-live-base"
		Provide(addr, &base.SingleThread{})
		c := NewClient(addr, true, nil, false)

		stream, err := c.AudioTranscriptionLive(context.Background())
		Expect(err).NotTo(HaveOccurred())

		Expect(stream.Send(&pb.TranscriptLiveRequest{
			Payload: &pb.TranscriptLiveRequest_Config{Config: &pb.TranscriptLiveConfig{}},
		})).To(Succeed())

		_, err = stream.Recv()
		Expect(err).To(HaveOccurred())
		Expect(grpcerrors.IsLiveTranscriptionUnsupported(err)).To(BeTrue())

		// Degrading callers close the send side; in embed mode this is also
		// what unwinds the server-side recv pump.
		Expect(stream.CloseSend()).To(Succeed())
	})
})
