package main

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/streamer45/silero-vad-go/speech"
)

type VAD struct {
	base.SingleThread
	detector *speech.Detector
}

func (vad *VAD) Load(opts *pb.ModelOptions) error {
	v, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:  opts.ModelFile,
		SampleRate: 16000,
		//WindowSize:           1024,
		Threshold:            0.5,
		MinSilenceDurationMs: 0,
		SpeechPadMs:          0,
	})
	if err != nil {
		return fmt.Errorf("create silero detector: %w", err)
	}

	vad.detector = v
	return err
}

func (vad *VAD) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	audio := req.Audio

	segments, err := vad.detector.Detect(audio)
	if err != nil {
		return pb.VADResponse{}, fmt.Errorf("detect: %w", err)
	}

	vadSegments := []*pb.VADSegment{}
	for _, s := range segments {
		vadSegments = append(vadSegments, &pb.VADSegment{
			Start: float32(s.SpeechStartAt),
			End:   float32(s.SpeechEndAt),
		})
	}

	return pb.VADResponse{
		Segments: vadSegments,
	}, nil
}
