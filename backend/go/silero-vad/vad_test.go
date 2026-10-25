package main

import (
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVADOptions(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Silero VAD Options")
}

var _ = Describe("Detector configuration", func() {
	It("preserves the model path and defaults", func() {
		cfg := detectorConfigFromOptions(&pb.ModelOptions{ModelFile: "silero_vad.onnx"})
		Expect(cfg.ModelPath).To(Equal("silero_vad.onnx"))
		Expect(cfg.SampleRate).To(Equal(16000))
		Expect(cfg.Threshold).To(Equal(float32(0.5)))
		Expect(cfg.MinSilenceDurationMs).To(Equal(100))
		Expect(cfg.SpeechPadMs).To(Equal(30))
	})

	It("applies model options", func() {
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
		Expect(cfg.Threshold).To(Equal(float32(0.55)))
		Expect(cfg.MinSilenceDurationMs).To(Equal(50))
		Expect(cfg.SpeechPadMs).To(Equal(450))
	})

	It("ignores NaN and malformed values without replacing valid options", func() {
		cfg := detectorConfigFromOptions(&pb.ModelOptions{
			ModelFile: "silero_vad.onnx",
			Options: []string{
				"threshold:0.55",
				"threshold:NaN",
				"threshold:abc",
				"min_silence_duration_ms:-1",
				"speech_pad_ms:abc",
			},
		})
		Expect(cfg.Threshold).To(Equal(float32(0.55)))
		Expect(cfg.MinSilenceDurationMs).To(Equal(100))
		Expect(cfg.SpeechPadMs).To(Equal(30))
		Expect(cfg.IsValid()).To(Succeed())
	})

	It("uses the default threshold when the only override is NaN", func() {
		cfg := detectorConfigFromOptions(&pb.ModelOptions{
			ModelFile: "silero_vad.onnx",
			Options:   []string{"threshold:NaN"},
		})
		Expect(cfg.Threshold).To(Equal(float32(0.5)))
		Expect(cfg.IsValid()).To(Succeed())
	})
})
