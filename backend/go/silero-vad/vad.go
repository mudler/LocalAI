package main

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/streamer45/silero-vad-go/speech"
)

const (
	defaultThreshold            = 0.5
	defaultMinSilenceDurationMs = 100
	defaultSpeechPadMs          = 30
)

type VAD struct {
	base.SingleThread
	detector *speech.Detector
}

func (vad *VAD) Load(opts *pb.ModelOptions) error {
	cfg := detectorConfigFromOptions(opts)

	v, err := speech.NewDetector(cfg)
	if err != nil {
		return fmt.Errorf("create silero detector: %w", err)
	}

	vad.detector = v
	return nil
}

func detectorConfigFromOptions(opts *pb.ModelOptions) speech.DetectorConfig {
	cfg := speech.DetectorConfig{
		ModelPath:            opts.ModelFile,
		SampleRate:           16000,
		Threshold:            defaultThreshold,
		MinSilenceDurationMs: defaultMinSilenceDurationMs,
		SpeechPadMs:          defaultSpeechPadMs,
	}

	for _, opt := range opts.Options {
		key, value, ok := strings.Cut(opt, ":")
		if !ok || value == "" {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(key)) {
		case "threshold":
			if v, err := strconv.ParseFloat(strings.TrimSpace(value), 32); err == nil && !math.IsNaN(v) {
				cfg.Threshold = float32(v)
			}
		case "min_silence_duration_ms":
			if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && v >= 0 {
				cfg.MinSilenceDurationMs = v
			}
		case "speech_pad_ms":
			if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && v >= 0 {
				cfg.SpeechPadMs = v
			}
		}
	}

	return cfg
}

func (vad *VAD) VAD(req *pb.VADRequest) (pb.VADResponse, error) {
	audio := req.Audio

	if err := vad.detector.Reset(); err != nil {
		return pb.VADResponse{}, fmt.Errorf("reset: %w", err)
	}

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
