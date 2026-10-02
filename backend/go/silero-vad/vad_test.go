package main

import (
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

func TestDetectorConfigFromOptionsDefaults(t *testing.T) {
	cfg := detectorConfigFromOptions(&pb.ModelOptions{ModelFile: "silero_vad.onnx"})

	if cfg.ModelPath != "silero_vad.onnx" {
		t.Fatalf("ModelPath = %q, want silero_vad.onnx", cfg.ModelPath)
	}
	if cfg.SampleRate != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", cfg.SampleRate)
	}
	if cfg.Threshold != defaultThreshold {
		t.Fatalf("Threshold = %v, want %v", cfg.Threshold, defaultThreshold)
	}
	if cfg.MinSilenceDurationMs != defaultMinSilenceDurationMs {
		t.Fatalf("MinSilenceDurationMs = %d, want %d", cfg.MinSilenceDurationMs, defaultMinSilenceDurationMs)
	}
	if cfg.SpeechPadMs != defaultSpeechPadMs {
		t.Fatalf("SpeechPadMs = %d, want %d", cfg.SpeechPadMs, defaultSpeechPadMs)
	}
}

func TestDetectorConfigFromOptionsOverrides(t *testing.T) {
	cfg := detectorConfigFromOptions(&pb.ModelOptions{
		ModelFile: "silero_vad.onnx",
		Options: []string{
			"threshold:0.55",
			"min_silence_duration_ms:50",
			"speech_pad_ms:450",
			"ignored",
			"bad_threshold:abc",
		},
	})

	if cfg.Threshold != 0.55 {
		t.Fatalf("Threshold = %v, want 0.55", cfg.Threshold)
	}
	if cfg.MinSilenceDurationMs != 50 {
		t.Fatalf("MinSilenceDurationMs = %d, want 50", cfg.MinSilenceDurationMs)
	}
	if cfg.SpeechPadMs != 450 {
		t.Fatalf("SpeechPadMs = %d, want 450", cfg.SpeechPadMs)
	}
}
