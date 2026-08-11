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

	// The gallery ships one concrete image per hardware capability behind a
	// meta name, and an operator may pin any of them in a model's `backend:`.
	// An exact-match-only lookup silently treated every one of them as an
	// unknown backend, which cost vulkan-localvqe the 16 kHz mono fold its AEC
	// needs and would cost a pinned audio-cpp variant its voice-cloning
	// contract. Same class as #10945.
	It("resolves a pinned hardware variant to its meta backend", func() {
		for _, name := range []string{"cpu-localvqe", "vulkan-localvqe", "metal-localvqe"} {
			capability := GetBackendCapability(name)
			Expect(capability).NotTo(BeNil(), "pinned variant %q must resolve", name)
			Expect(capability.PossibleUsecases).To(ContainElement(UsecaseAudioTransform), name)
		}
	})

	It("resolves a pinned variant that also carries a release channel", func() {
		for _, name := range []string{
			"cuda12-audio-cpp", "cuda13-audio-cpp-development",
			"metal-audio-cpp", "cpu-audio-cpp-development",
			"cuda13-nvidia-l4t-arm64-llama-cpp", "intel-sycl-f16-llama-cpp",
			"metal-darwin-arm64-llama-cpp", "nvidia-l4t-arm64-llama-cpp",
			"rocm-llama-cpp-development", "intel-llama-cpp",
		} {
			Expect(GetBackendCapability(name)).NotTo(BeNil(), "pinned variant %q must resolve", name)
		}
	})

	It("does not invent a capability for a name that only looks like a variant", func() {
		Expect(GetBackendCapability("cpu-nonexistent")).To(BeNil())
		Expect(GetBackendCapability("vulkan-")).To(BeNil())
	})

	// Stripping is a fallback, never a rewrite: a backend registered under its
	// own name keeps its own entry even if that name starts with a prefix.
	It("prefers an exact match over the stripped one", func() {
		BackendCapabilities["cpu-exact-match-probe"] = BackendCapability{
			PossibleUsecases: []string{UsecaseChat},
			Description:      "test fixture",
		}
		BackendCapabilities["exact-match-probe"] = BackendCapability{
			PossibleUsecases: []string{UsecaseTTS},
			Description:      "test fixture",
		}
		DeferCleanup(func() {
			delete(BackendCapabilities, "cpu-exact-match-probe")
			delete(BackendCapabilities, "exact-match-probe")
		})

		capability := GetBackendCapability("cpu-exact-match-probe")
		Expect(capability).NotTo(BeNil())
		Expect(capability.PossibleUsecases).To(Equal([]string{UsecaseChat}))
	})
})

// nemo-speech-cpp fronts four model families from one server, and its
// PossibleUsecases is their union. The entry has to stay in step with what
// docs/content/features/nemo-speech-cpp.md tells operators to put in
// known_usecases: nothing validates known_usecases against PossibleUsecases, so
// a flag the docs recommend and the map omits fails silently, and the place it
// surfaces is the gallery. GET /api/backends/usecases is derived from this list
// and greys out the filters missing from it, so a recommended-but-unlisted flag
// hides the very models it was recommended for.
var _ = Describe("nemo-speech-cpp capabilities", func() {
	It("advertises every usecase its four families serve", func() {
		capability := GetBackendCapability("nemo-speech-cpp")
		Expect(capability).NotTo(BeNil())
		Expect(capability.PossibleUsecases).To(ContainElements(
			UsecaseTranscript, UsecaseDiarization, UsecaseTTS,
			UsecaseCompletion, UsecaseChat))
	})

	// Chat is the translation family's usecase, and it needs both Predict RPCs:
	// /v1/chat/completions streams through PredictStream and answers
	// non-streaming requests through Predict.
	It("backs the chat usecase with the RPCs chat actually drives", func() {
		capability := GetBackendCapability("nemo-speech-cpp")
		Expect(capability.GRPCMethods).To(ContainElements(MethodPredict, MethodPredictStream))
	})

	// Defaults stay conservative: a bare `backend: nemo-speech-cpp` with no
	// known_usecases is overwhelmingly an ASR model, and every other family is
	// expected to pin its own flags.
	It("still defaults to transcript alone", func() {
		Expect(DefaultUsecasesForBackendCap("nemo-speech-cpp")).To(Equal([]string{UsecaseTranscript}))
	})
})

// audio-cpp advertises voice cloning from the backend itself and ships
// audio-cpp-chatterbox, whose family serves cloning and NOT plain TTS, so a
// reference clip is the only way to use it. Without a capability entry
// VoiceCloningForModel returns nil before it ever reads the model's own
// tts.voice_cloning override, so `voice: "profile:<id>"` was refused with a 400
// for every audio-cpp model and no model YAML could rescue it.
var _ = Describe("audio-cpp capabilities", func() {
	It("is registered", func() {
		Expect(GetBackendCapability("audio-cpp")).NotTo(BeNil())
	})

	It("advertises the RPCs its families actually serve", func() {
		capability := GetBackendCapability("audio-cpp")
		Expect(capability.GRPCMethods).To(ContainElements(
			MethodTTS, MethodTTSStream, MethodAudioTranscription,
			MethodVAD, MethodDiarize, MethodSoundGeneration, MethodAudioTransform))
		Expect(capability.PossibleUsecases).To(ContainElements(
			UsecaseTTS, UsecaseTranscript, UsecaseVAD, UsecaseDiarization,
			UsecaseSoundGeneration, UsecaseAudioTransform))
	})

	It("carries the reference-audio contract, for pinned variants too", func() {
		for _, name := range []string{"audio-cpp", "cuda12-audio-cpp", "metal-audio-cpp"} {
			cloning := VoiceCloningForModel(&ModelConfig{Backend: name})
			Expect(cloning).NotTo(BeNil(), "%q must reach the backend with a profile voice", name)
			Expect(cloning.AcceptedAudioFormats).To(ContainElement("audio/wav"))
		}
	})

	// The families audio-cpp reaches through AudioTransform are separation and
	// conversion, which refuse any rate but their checkpoint's own.
	It("does not ask for the 16 kHz mono fold", func() {
		Expect(AudioTransformRequiresMono16kInput("audio-cpp")).To(BeFalse())
	})
})

// The fold to 16 kHz mono in /audio/transform is opt-IN. It used to be
// unconditional, which made source separation unreachable through the HTTP
// API: htdemucs and mel_band_roformer refuse any rate but their checkpoint's
// own and separate using the stereo image, so every such request died with an
// INTERNAL raised inside the engine while the same call over gRPC worked.
var _ = Describe("AudioTransformRequiresMono16kInput", func() {
	It("folds for localvqe, whose AEC is trained on 16 kHz mono", func() {
		Expect(AudioTransformRequiresMono16kInput("localvqe")).To(BeTrue())
	})

	// Pinned gallery variants are the same engine and must fold identically.
	// They did not: the lookup was exact-match only, so vulkan-localvqe was an
	// unknown backend, lost the fold that used to be unconditional, and started
	// failing inside LocalVQE. The usecase gate is no substitute, because
	// BuildFilteredFirstAvailableDefaultModel returns early once the client
	// names a model explicitly.
	It("folds for every pinned localvqe variant the gallery ships", func() {
		Expect(AudioTransformRequiresMono16kInput("cpu-localvqe")).To(BeTrue())
		Expect(AudioTransformRequiresMono16kInput("vulkan-localvqe")).To(BeTrue())
		Expect(AudioTransformRequiresMono16kInput("metal-localvqe")).To(BeTrue())
	})

	It("does not fold for a backend that has not asked for it", func() {
		// Registered, and deliberately NOT folding: its separation and
		// conversion families refuse any rate but their checkpoint's own.
		Expect(AudioTransformRequiresMono16kInput("audio-cpp")).To(BeFalse())
		Expect(AudioTransformRequiresMono16kInput("nonexistent")).To(BeFalse())
		Expect(AudioTransformRequiresMono16kInput("")).To(BeFalse())
	})

	It("is claimed by no other registered backend", func() {
		for name, capability := range BackendCapabilities {
			if name == "localvqe" {
				continue
			}
			Expect(capability.AudioTransformInputMono16k).To(BeFalse(),
				"backend %q asks for the 16 kHz mono fold; that has to be a deliberate, documented need", name)
		}
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

	// A pinned gallery variant must reach the SAME per-backend rule as the meta
	// name, in both directions. Resolving the capability by stripping the prefix
	// while still keying the model-variant switch on the pinned spelling made
	// every variant fall through to the permissive default: cuda12-vibevoice-cpp
	// advertised cloning for the realtime 0.5B model, which cannot do it, and
	// /v1/audio/speech accepted a profile: voice it had to fail on in the backend
	// instead of rejecting it with a 400.
	DescribeTable("resolves the model-variant rule through pinned gallery variants",
		func(cfg ModelConfig, expected bool) {
			Expect(VoiceCloningForModel(&cfg) != nil).To(Equal(expected))
		},
		Entry("cuda12-vibevoice-cpp 0.5B stays unsupported", ModelConfig{Name: "vibevoice-cpp-0.5b", Backend: "cuda12-vibevoice-cpp"}, false),
		Entry("cuda12-vibevoice-cpp 1.5B stays supported", ModelConfig{Name: "vibevoice-1.5b", Backend: "cuda12-vibevoice-cpp"}, true),
		Entry("metal-coqui tacotron2 stays unsupported", ModelConfig{Name: "tacotron2-en", Backend: "metal-coqui"}, false),
		Entry("metal-coqui xtts stays supported", ModelConfig{Name: "xtts-v2", Backend: "metal-coqui"}, true),
		Entry("cuda12-crispasr ASR stays unsupported", ModelConfig{Name: "parakeet-asr", Backend: "cuda12-crispasr"}, false),
		Entry("cuda12-crispasr F5 stays supported", ModelConfig{Name: "f5-tts-crispasr", Backend: "cuda12-crispasr"}, true),
		Entry("cpu-qwen3-tts-cpp CustomVoice stays unsupported", ModelConfig{Name: "qwen3-tts-flash", Backend: "cpu-qwen3-tts-cpp"}, false),
		Entry("cpu-qwen3-tts-cpp Base stays supported", ModelConfig{Name: "qwen3-tts-cpp-0.6b-base", Backend: "cpu-qwen3-tts-cpp"}, true),
		Entry("release channel suffix too", ModelConfig{Name: "vibevoice-cpp-0.5b", Backend: "vibevoice-cpp-development"}, false),
		Entry("pinned audio-cpp keeps its unconditional cloning", ModelConfig{Name: "audio-cpp-chatterbox", Backend: "cuda12-audio-cpp"}, true),
	)
})

// llama.cpp serves Qwen3-TTS as well as the text LLMs it is known for, so the
// backend has to advertise TTS. That advertisement is what makes narrowing
// mandatory: the per-backend switch in VoiceCloningForModel ends in a
// permissive default, so an unnarrowed llama-cpp entry would offer
// reference-audio cloning on every GGUF chat model in the gallery.
var _ = Describe("llama-cpp TTS capabilities", func() {
	It("advertises the TTS RPCs and usecase", func() {
		capability := GetBackendCapability("llama-cpp")
		Expect(capability).NotTo(BeNil())
		Expect(capability.GRPCMethods).To(ContainElements(MethodTTS, MethodTTSStream))
		Expect(capability.PossibleUsecases).To(ContainElement(UsecaseTTS))
	})

	// The gallery filter and the model importer both read DefaultUsecases, and a
	// bare GGUF served by llama.cpp is a chat model, not a TTS model.
	It("keeps chat as its only default usecase", func() {
		Expect(GetBackendCapability("llama-cpp").DefaultUsecases).To(Equal([]string{UsecaseChat}))
	})

	ttsModel := func(backend string) ModelConfig {
		cfg := ModelConfig{Name: "qwen3-tts-llamacpp", Backend: backend}
		cfg.KnownUsecaseStrings = []string{"tts"}
		cfg.syncKnownUsecasesFromString()
		return cfg
	}

	It("resolves voice cloning for a model that declares the TTS usecase", func() {
		cfg := ttsModel("llama-cpp")
		cloning := VoiceCloningForModel(&cfg)
		Expect(cloning).NotTo(BeNil())
		Expect(cloning.AcceptedAudioFormats).To(ContainElement("audio/wav"))
	})

	// The spec that constrains the fix. Every one of these is an ordinary
	// llama.cpp text model, and none of them may be offered in the Voice
	// Library or accept a localai://voice-profiles/... reference.
	DescribeTable("never resolves voice cloning for an ordinary llama.cpp model",
		func(cfg ModelConfig) {
			Expect(VoiceCloningForModel(&cfg)).To(BeNil())
		},
		Entry("plain chat model", ModelConfig{Name: "qwen3-8b", Backend: "llama-cpp"}),
		Entry("auto-detected GGUF with no backend pinned", ModelConfig{Name: "mistral-7b"}),
		Entry("a vision model with an mmproj", ModelConfig{Name: "gemma-3-12b", Backend: "llama-cpp", LLMConfig: LLMConfig{MMProj: "mmproj-gemma-3-12b.gguf"}}),
		Entry("a chat model whose name happens to say base", ModelConfig{Name: "llama-3.1-8b-base", Backend: "llama-cpp"}),
		Entry("a pinned hardware variant", ModelConfig{Name: "qwen3-8b", Backend: "cuda12-llama-cpp"}),
	)

	// A declared-TTS model must keep its contract through the pinned gallery
	// variants an operator can put in `backend:`, the same way vibevoice-cpp
	// and crispasr do.
	DescribeTable("resolves through pinned gallery variants",
		func(backend string) {
			cfg := ttsModel(backend)
			Expect(VoiceCloningForModel(&cfg)).NotTo(BeNil())
		},
		Entry("cuda12", "cuda12-llama-cpp"),
		Entry("vulkan", "vulkan-llama-cpp"),
		Entry("metal darwin arm64", "metal-darwin-arm64-llama-cpp"),
		Entry("development channel", "llama-cpp-development"),
	)

	// tts.voice_cloning is the documented escape hatch for a custom build. It
	// only ever reaches the operator once the backend carries the contract at
	// all, which is precisely what the unregistered entry prevented.
	It("still honours an explicit opt-out on a declared-TTS model", func() {
		cfg := ttsModel("llama-cpp")
		disabled := false
		cfg.TTSConfig.VoiceCloning = &disabled
		Expect(VoiceCloningForModel(&cfg)).To(BeNil())
	})
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
