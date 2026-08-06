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

		It("is false for a TTS model whose mmproj is a speaker encoder", func() {
			// Qwen3-TTS on llama-cpp ships an mmproj that holds the speaker
			// encoder and code predictor, not a vision tower.
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_TTS), Backend: "llama-cpp"}
			cfg.MMProj = "mmproj-Qwen3-TTS-12Hz-1.7B-Base-Q8_0.gguf"
			Expect(cfg.VisionSupported()).To(BeFalse())
		})

		It("is false for a TTS model whose backend reported a media marker", func() {
			// llama.cpp builds an mtmd context for the speaker-encoder projector
			// and reports its marker on the first chat probe, which would
			// otherwise resurrect vision after the model has been used once.
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_TTS), Backend: "llama-cpp"}
			cfg.MMProj = "mmproj-Qwen3-TTS-12Hz-1.7B-Base-Q8_0.gguf"
			cfg.MediaMarker = "<__media__>"
			Expect(cfg.VisionSupported()).To(BeFalse())
		})

		It("is still true for a TTS model that also declares vision", func() {
			// An omni model can legitimately be both. The explicit bit wins.
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_TTS | FLAG_VISION), Backend: "llama-cpp"}
			cfg.MMProj = "mmproj.gguf"
			Expect(cfg.VisionSupported()).To(BeTrue())
		})

		It("does not fall for the GuessUsecases FLAG_VISION false positive", func() {
			// A chat model with a chat template would make HasUsecases(FLAG_VISION)
			// return true via the guess heuristic; VisionSupported must not.
			cfg := &ModelConfig{Backend: "llama.cpp"}
			cfg.TemplateConfig.Chat = "{{.Input}}"
			Expect(cfg.VisionSupported()).To(BeFalse())
		})

		It("survives the loader re-syncing known_usecases from the rewritten list", func() {
			// syncKnownUsecasesFromString rewrites KnownUsecaseStrings from
			// HasUsecases, and the loader calls it more than once per file. If a
			// guessed "vision" leaks into that list, the next pass parses it back
			// into KnownUsecases as an explicit bit and the mmproj exemption above
			// is bypassed. Reproduces the gallery entry qwen3-tts-llamacpp-q4.
			cfg := &ModelConfig{Backend: "llama-cpp"}
			cfg.KnownUsecaseStrings = []string{"tts"}
			cfg.MMProj = "qwen3-tts-llamacpp-q4/mmproj-Qwen3-TTS-12Hz-1.7B-Base-Q8_0.gguf"
			cfg.TemplateConfig.UseTokenizerTemplate = true

			cfg.syncKnownUsecasesFromString()
			cfg.syncKnownUsecasesFromString()

			Expect(cfg.KnownUsecaseStrings).NotTo(ContainElement("FLAG_VISION"))
			Expect(cfg.VisionSupported()).To(BeFalse())
			Expect(cfg.Capabilities()).NotTo(ContainElement(UsecaseVision))
			Expect(cfg.InputModalities()).NotTo(ContainElement(ModalityImage))
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

		It("guesses the 3d usecase from the trellis2cpp backend and only that backend", func() {
			cfg := &ModelConfig{Backend: "trellis2cpp"}
			Expect(cfg.HasUsecases(FLAG_3D)).To(BeTrue())
			Expect(cfg.Capabilities()).To(ContainElement(Usecase3D))

			other := &ModelConfig{Backend: "llama-cpp"}
			Expect(other.HasUsecases(FLAG_3D)).To(BeFalse())
		})

		It("a 3D-generation model reads an image and writes a 3D asset", func() {
			// Pins the wire strings the UI depends on: capability "3d",
			// input modality "image" (no text prompt — TRELLIS.2 is
			// image-conditioned only), output modality "3d".
			cfg := &ModelConfig{KnownUsecases: usecaseBits(FLAG_3D), Backend: "trellis2cpp"}
			Expect(cfg.Capabilities()).To(Equal([]string{Usecase3D}))
			Expect(cfg.InputModalities()).To(Equal([]string{ModalityImage}))
			Expect(cfg.OutputModalities()).To(Equal([]string{Modality3D}))
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
