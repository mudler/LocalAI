package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func usecaseBits(flags ModelConfigUsecase) *ModelConfigUsecase {
	return &flags
}

var _ = Describe("Model capabilities derivation", func() {
	Describe("VisionSupported", func() {
		It("is false for a plain text chat model", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT), Backend: "llama.cpp"}
			Expect(cfg.VisionSupported()).To(BeFalse())
		})

		It("is true when the FLAG_VISION bit is declared", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT | FLAG_VISION), Backend: "llama.cpp"}
			Expect(cfg.VisionSupported()).To(BeTrue())
		})

		It("is true when image input is declared explicitly", func() {
			cfg := &ModelConfig{
				KnownUsecases:        usecaseBits(FLAG_CHAT),
				KnownInputModalities: []string{ModalityText, ModalityImage},
			}
			Expect(cfg.VisionSupported()).To(BeTrue())
		})

		It("is true when an mmproj projector is set", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT), Backend: "llama.cpp"}
			cfg.MMProj = "mmproj.gguf" // promoted field from the embedded options struct
			Expect(cfg.VisionSupported()).To(BeTrue())
		})

		It("does not fall for the GuessUsecases FLAG_VISION false positive", func() {
			// A chat model with a chat template would make HasUsecases(FLAG_VISION)
			// return true via the guess heuristic; VisionSupported must not.
			cfg := &ModelConfig{Backend: "llama.cpp"}
			cfg.TemplateConfig.Chat = "{{.Input}}"
			Expect(cfg.VisionSupported()).To(BeFalse())
		})
	})

	Describe("AudioInputSupported / VideoInputSupported", func() {
		It("honors explicit model modality declarations", func() {
			cfg := &ModelConfig{
				KnownInputModalities: []string{ModalityAudio, ModalityVideo},
			}
			Expect(cfg.AudioInputSupported()).To(BeTrue())
			Expect(cfg.VideoInputSupported()).To(BeTrue())
		})

		It("detects vLLM omni audio input via limit_mm_per_prompt", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT), Backend: "vllm"}
			cfg.LimitMMPerPrompt.LimitAudioPerPrompt = 1
			Expect(cfg.AudioInputSupported()).To(BeTrue())
			Expect(cfg.VideoInputSupported()).To(BeFalse())
		})

		It("detects vLLM omni video input via limit_mm_per_prompt", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT), Backend: "vllm"}
			cfg.LimitMMPerPrompt.LimitVideoPerPrompt = 2
			Expect(cfg.VideoInputSupported()).To(BeTrue())
		})
	})

	Describe("Capabilities + modalities", func() {
		It("a text-only chat model exposes chat and text-only modalities", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT), Backend: "llama.cpp"}
			Expect(cfg.Capabilities()).To(ContainElement(UsecaseChat))
			Expect(cfg.Capabilities()).NotTo(ContainElement(UsecaseVision))
			Expect(cfg.Capabilities()).NotTo(ContainElement(UsecaseTranscript))
			Expect(cfg.InputModalities()).To(Equal([]string{"text"}))
			Expect(cfg.OutputModalities()).To(Equal([]string{"text"}))
		})

		It("a vision chat model accepts text+image input", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT | FLAG_VISION), Backend: "llama.cpp"}
			Expect(cfg.Capabilities()).To(ContainElements(UsecaseChat, UsecaseVision))
			Expect(cfg.InputModalities()).To(Equal([]string{"text", "image"}))
			Expect(cfg.OutputModalities()).To(Equal([]string{"text"}))
		})

		It("an omni chat model accepts text+audio input without an audio capability flag", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_CHAT), Backend: "vllm"}
			cfg.LimitMMPerPrompt.LimitAudioPerPrompt = 1
			// audio-in is a modality, not a usecase string — this is exactly the
			// case a plain capability list cannot express.
			Expect(cfg.Capabilities()).To(ContainElement(UsecaseChat))
			Expect(cfg.InputModalities()).To(Equal([]string{"text", "audio"}))
		})

		It("a transcription model reads audio and writes text", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_TRANSCRIPT), Backend: "parakeet-cpp"}
			Expect(cfg.Capabilities()).To(Equal([]string{UsecaseTranscript}))
			Expect(cfg.InputModalities()).To(Equal([]string{"audio"}))
			Expect(cfg.OutputModalities()).To(Equal([]string{"text"}))
		})

		It("an image-generation model reads text and writes an image", func() {
			// stablediffusion-ggml is image-only; plain "stablediffusion" is also
			// in GuessUsecases' video-backend list, so it would report video too.
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_IMAGE), Backend: "stablediffusion-ggml"}
			Expect(cfg.Capabilities()).To(Equal([]string{UsecaseImage}))
			Expect(cfg.InputModalities()).To(Equal([]string{"text"}))
			Expect(cfg.OutputModalities()).To(Equal([]string{"image"}))
		})

		It("conditioned video uses declared modalities without backend-specific inference", func() {
			cfg := &ModelConfig{
				KnownUsecases:         usecaseBits(FLAG_VIDEO),
				KnownInputModalities:  []string{ModalityAudio, ModalityImage, ModalityText, ModalityAudio, "unknown"},
				KnownOutputModalities: []string{ModalityVideo},
			}
			Expect(cfg.Capabilities()).To(Equal([]string{UsecaseVideo}))
			Expect(cfg.InputModalities()).To(Equal([]string{ModalityText, ModalityImage, ModalityAudio}))
			Expect(cfg.OutputModalities()).To(Equal([]string{ModalityVideo}))
		})

		It("a TTS model reads text and writes audio", func() {
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_TTS), Backend: "piper"}
			Expect(cfg.Capabilities()).To(ContainElement(UsecaseTTS))
			Expect(cfg.InputModalities()).To(Equal([]string{"text"}))
			Expect(cfg.OutputModalities()).To(Equal([]string{"audio"}))
		})
	})
})
