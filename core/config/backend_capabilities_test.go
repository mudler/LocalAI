package config

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BackendCapabilities", func() {
	It("every backend declares possible/default usecases and gRPC methods", func() {
		for name, cap := range BackendCapabilities {
			Expect(cap.PossibleUsecases).NotTo(BeEmpty(), "backend %q has no possible usecases", name)
			Expect(cap.DefaultUsecases).NotTo(BeEmpty(), "backend %q has no default usecases", name)
			Expect(cap.GRPCMethods).NotTo(BeEmpty(), "backend %q has no gRPC methods", name)
		}
	})

	It("default usecases are a subset of possible usecases", func() {
		for name, cap := range BackendCapabilities {
			for _, d := range cap.DefaultUsecases {
				Expect(cap.PossibleUsecases).To(ContainElement(d), "backend %q: default %q not in possible %v", name, d, cap.PossibleUsecases)
			}
		}
	})

	It("every backend's possible usecases map to a known FLAG_*", func() {
		allFlags := GetAllModelConfigUsecases()
		for name, cap := range BackendCapabilities {
			for _, u := range cap.PossibleUsecases {
				info, ok := UsecaseInfoMap[u]
				Expect(ok).To(BeTrue(), "backend %q: usecase %q not in UsecaseInfoMap", name, u)
				flagName := "FLAG_" + strings.ToUpper(u)
				if _, ok := allFlags[flagName]; ok {
					continue
				}
				// Some usecase names don't transform exactly to FLAG_<UPPER>; fall back to flag value lookup.
				found := false
				for _, flag := range allFlags {
					if flag == info.Flag {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "backend %q: usecase %q flag %d not in GetAllModelConfigUsecases", name, u, info.Flag)
			}
		}
	})

	It("every UsecaseInfoMap entry has a non-zero flag and a gRPC method", func() {
		for name, info := range UsecaseInfoMap {
			Expect(info.Flag).NotTo(Equal(FLAG_ANY), "usecase %q has FLAG_ANY (zero) — should have a real flag", name)
			Expect(info.GRPCMethod).NotTo(BeEmpty(), "usecase %q has no gRPC method", name)
		}
	})
})

var _ = Describe("GetBackendCapability", func() {
	It("returns the capability for a known backend", func() {
		cap := GetBackendCapability("llama-cpp")
		Expect(cap).NotTo(BeNil())
		Expect(cap.PossibleUsecases).To(ContainElement("chat"))
	})

	It("normalizes hyphenated names so llama.cpp resolves to llama-cpp", func() {
		Expect(GetBackendCapability("llama.cpp")).NotTo(BeNil())
	})

	It("returns nil for unknown backends", func() {
		Expect(GetBackendCapability("nonexistent")).To(BeNil())
	})
})

var _ = Describe("VoiceCloningForModel", func() {
	voiceCloningSetting := func(enabled bool) *bool { return &enabled }

	DescribeTable("advertises only compatible model variants",
		func(cfg ModelConfig, expected bool) {
			Expect(VoiceCloningForModel(&cfg) != nil).To(Equal(expected))
		},
		Entry("Qwen C++ Base", ModelConfig{Name: "qwen3-tts-cpp-0.6b-base", Backend: "qwen3-tts-cpp"}, true),
		Entry("Qwen C++ CustomVoice", ModelConfig{Name: "qwen3-tts-cpp-customvoice", Backend: "qwen3-tts-cpp"}, false),
		Entry("VibeVoice realtime 0.5B", ModelConfig{Name: "vibevoice-cpp-0.5b", Backend: "vibevoice-cpp"}, false),
		Entry("VibeVoice 1.5B", ModelConfig{Name: "vibevoice-1.5b", Backend: "vibevoice-cpp"}, true),
		Entry("F5 through CrispASR", ModelConfig{Name: "f5-tts-crispasr", Backend: "crispasr"}, true),
		Entry("ASR through CrispASR", ModelConfig{Name: "parakeet-crispasr", Backend: "crispasr"}, false),
		Entry("VoxCPM", ModelConfig{Name: "voxcpm-1.5", Backend: "voxcpm"}, true),
		Entry("unsupported Piper", ModelConfig{Name: "piper", Backend: "piper"}, false),
		Entry("typed custom opt-in", ModelConfig{Name: "private-build", Backend: "qwen3-tts-cpp", TTSConfig: TTSConfig{VoiceCloning: voiceCloningSetting(true)}}, true),
		Entry("typed opt-out", ModelConfig{Name: "voxcpm-1.5", Backend: "voxcpm", TTSConfig: TTSConfig{VoiceCloning: voiceCloningSetting(false)}}, false),
		Entry("typed setting wins over compatibility option", ModelConfig{Name: "private-build", Backend: "qwen3-tts-cpp", TTSConfig: TTSConfig{VoiceCloning: voiceCloningSetting(true)}, Options: []string{"voice_cloning:false"}}, true),
		Entry("legacy option custom opt-in", ModelConfig{Name: "private-build", Backend: "qwen3-tts-cpp", Options: []string{"voice_cloning:true"}}, true),
		Entry("legacy option opt-out", ModelConfig{Name: "voxcpm-1.5", Backend: "voxcpm", Options: []string{"voice_cloning=false"}}, false),
	)
})

var _ = Describe("IsValidUsecaseForBackend", func() {
	It("accepts a backend's declared usecases", func() {
		Expect(IsValidUsecaseForBackend("piper", "tts")).To(BeTrue())
	})

	It("rejects usecases outside a backend's possible set", func() {
		Expect(IsValidUsecaseForBackend("piper", "chat")).To(BeFalse())
	})

	It("is permissive for unknown backends", func() {
		Expect(IsValidUsecaseForBackend("unknown", "anything")).To(BeTrue())
	})
})

var _ = Describe("IsLlamaCppBackend", func() {
	DescribeTable("classifies a backend name",
		func(backend string, expected bool) {
			Expect(IsLlamaCppBackend(backend)).To(Equal(expected))
		},
		Entry("meta name", "llama-cpp", true),
		Entry("dotted spelling", "llama.cpp", true),
		Entry("auto-detect (empty)", "", true),
		Entry("development channel", "llama-cpp-development", true),
		Entry("quantization channel", "llama-cpp-quantization", true),
		Entry("vulkan variant", "vulkan-llama-cpp", true),
		Entry("cuda 12 variant", "cuda12-llama-cpp", true),
		Entry("cuda 13 variant", "cuda13-llama-cpp", true),
		Entry("jetson variant", "cuda13-nvidia-l4t-arm64-llama-cpp", true),
		Entry("rocm variant", "rocm-llama-cpp", true),
		Entry("metal variant", "metal-llama-cpp", true),
		Entry("intel sycl f16 variant", "intel-sycl-f16-llama-cpp", true),
		Entry("intel sycl f32 variant", "intel-sycl-f32-llama-cpp", true),
		Entry("cpu variant", "cpu-llama-cpp", true),
		Entry("variant on the development channel", "rocm-llama-cpp-development", true),
		Entry("darwin quantization variant", "metal-darwin-arm64-llama-cpp-quantization", true),
		// ik-llama.cpp is a distinct engine that merely shares the suffix.
		Entry("ik-llama-cpp", "ik-llama-cpp", false),
		Entry("ik-llama-cpp development", "ik-llama-cpp-development", false),
		Entry("cpu ik-llama-cpp", "cpu-ik-llama-cpp", false),
		Entry("cpu ik-llama-cpp development", "cpu-ik-llama-cpp-development", false),
		Entry("vllm", "vllm", false),
		Entry("mlx", "mlx", false),
		Entry("whisper", "whisper", false),
		Entry("bark-cpp", "bark-cpp", false),
	)
})

var _ = Describe("AllBackendNames", func() {
	It("returns 30+ backends in sorted order", func() {
		names := AllBackendNames()
		Expect(len(names)).To(BeNumerically(">=", 30))
		Expect(slices.IsSorted(names)).To(BeTrue())
	})
})
