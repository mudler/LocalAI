package system

import (
	"os"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("getSystemCapabilities", func() {
	const eightGB = 8 * 1024 * 1024 * 1024
	const twoGB = 2 * 1024 * 1024 * 1024

	var (
		origEnv    string
		origCuda12 bool
		origCuda13 bool
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("darwin short-circuits before reaching CUDA logic")
		}

		origEnv = os.Getenv(capabilityEnv)
		os.Unsetenv(capabilityEnv)

		origCuda12 = cuda12DirExists
		origCuda13 = cuda13DirExists
	})

	AfterEach(func() {
		cuda12DirExists = origCuda12
		cuda13DirExists = origCuda13

		if origEnv != "" {
			os.Setenv(capabilityEnv, origEnv)
		}
	})

	type testCase struct {
		gpuVendor      string
		vram           uint64
		cuda12         bool
		cuda13         bool
		wantCapability string
		wantTokens     []string
	}

	DescribeTable("capability detection",
		func(tc testCase) {
			cuda12DirExists = tc.cuda12
			cuda13DirExists = tc.cuda13

			s := &SystemState{
				GPUVendor: tc.gpuVendor,
				VRAM:      tc.vram,
			}

			Expect(s.getSystemCapabilities()).To(Equal(tc.wantCapability))
			Expect(s.BackendPreferenceTokens()).To(Equal(tc.wantTokens))
		},
		Entry("CUDA dir present but no GPU", testCase{
			gpuVendor:      "",
			vram:           0,
			cuda12:         true,
			cuda13:         false,
			wantCapability: "default",
			wantTokens:     []string{"cpu"},
		}),
		Entry("CUDA 12 with NVIDIA GPU", testCase{
			gpuVendor:      Nvidia,
			vram:           eightGB,
			cuda12:         true,
			cuda13:         false,
			wantCapability: "nvidia-cuda-12",
			wantTokens:     []string{"cuda", "vulkan", "cpu"},
		}),
		Entry("CUDA 13 with NVIDIA GPU", testCase{
			gpuVendor:      Nvidia,
			vram:           eightGB,
			cuda12:         false,
			cuda13:         true,
			wantCapability: "nvidia-cuda-13",
			wantTokens:     []string{"cuda", "vulkan", "cpu"},
		}),
		Entry("Both CUDA dirs with NVIDIA GPU prefers 13", testCase{
			gpuVendor:      Nvidia,
			vram:           eightGB,
			cuda12:         true,
			cuda13:         true,
			wantCapability: "nvidia-cuda-13",
			wantTokens:     []string{"cuda", "vulkan", "cpu"},
		}),
		Entry("CUDA dir with AMD GPU ignored", testCase{
			gpuVendor:      AMD,
			vram:           eightGB,
			cuda12:         true,
			cuda13:         false,
			wantCapability: "amd",
			wantTokens:     []string{"rocm", "hip", "vulkan", "cpu"},
		}),
		Entry("No CUDA dir and no GPU", testCase{
			gpuVendor:      "",
			vram:           0,
			cuda12:         false,
			cuda13:         false,
			wantCapability: "default",
			wantTokens:     []string{"cpu"},
		}),
		Entry("No CUDA dir with NVIDIA GPU", testCase{
			gpuVendor:      Nvidia,
			vram:           eightGB,
			cuda12:         false,
			cuda13:         false,
			wantCapability: "nvidia",
			wantTokens:     []string{"cuda", "vulkan", "cpu"},
		}),
		Entry("CUDA dir with NVIDIA GPU but low VRAM", testCase{
			gpuVendor:      Nvidia,
			vram:           twoGB,
			cuda12:         true,
			cuda13:         false,
			wantCapability: "default",
			wantTokens:     []string{"cpu"},
		}),
	)
})

var _ = Describe("BackendPreferenceTokens", func() {
	var origEnv string

	BeforeEach(func() {
		origEnv = os.Getenv(capabilityEnv)
	})

	AfterEach(func() {
		if origEnv != "" {
			os.Setenv(capabilityEnv, origEnv)
		} else {
			os.Unsetenv(capabilityEnv)
		}
	})

	tokensFor := func(capability string) []string {
		GinkgoHelper()
		Expect(os.Setenv(capabilityEnv, capability)).To(Succeed())
		return (&SystemState{}).BackendPreferenceTokens()
	}

	// This table is a REGRESSION LOCK, not a description. These are build tags
	// matched against installed backend build directory names, and the only
	// consumer is alias resolution in ListSystemBackends. Every value below is
	// the output this function had before engine preference existed.
	//
	// Engine names ("vllm", "mlx", "llama-cpp") belong to EnginePreferenceTokens
	// and MUST NOT appear here. Merging the two vocabularies is exactly the
	// mistake this table exists to catch: it does not error, it silently makes
	// one of the two consumers rank everything equally.
	DescribeTable("returns the original build tags for every capability",
		func(capability string, want []string) {
			Expect(tokensFor(capability)).To(Equal(want))
		},
		Entry("nvidia", "nvidia-cuda-12", []string{"cuda", "vulkan", "cpu"}),
		Entry("amd", AMD, []string{"rocm", "hip", "vulkan", "cpu"}),
		Entry("intel", Intel, []string{"sycl", "intel", "cpu"}),
		Entry("metal", metal, []string{"metal", "cpu"}),
		Entry("darwin-x86", darwinX86, []string{"darwin-x86", "cpu"}),
		Entry("vulkan", vulkan, []string{"vulkan", "cpu"}),
		Entry("unknown capability", "some-future-accelerator", []string{"cpu"}),
	)

	It("carries no engine name in any capability's tokens", func() {
		// The generic form of the lock above: whatever anyone adds to the build
		// tag table later, an engine name in it is a merged vocabulary.
		engineNames := []string{"vllm", "sglang", "llama-cpp", "mlx"}
		for _, capability := range []string{"nvidia", AMD, Intel, metal, darwinX86, vulkan, "default"} {
			Expect(tokensFor(capability)).ToNot(ContainElements(engineNames),
				"capability %q leaked an engine name into the build tag table", capability)
		}
	})

	It("matches a capability by prefix so a refined one keeps its vendor order", func() {
		Expect(tokensFor("nvidia-l4t-cuda-13")).To(Equal(tokensFor("nvidia")))
	})

	It("hands out a copy so one caller cannot corrupt another's lookup", func() {
		first := tokensFor("nvidia")
		first[0] = "clobbered"
		Expect(tokensFor("nvidia")[0]).To(Equal("cuda"))
	})
})

var _ = Describe("EnginePreferenceTokens", func() {
	var origEnv string

	BeforeEach(func() {
		origEnv = os.Getenv(capabilityEnv)
	})

	AfterEach(func() {
		if origEnv != "" {
			os.Setenv(capabilityEnv, origEnv)
		} else {
			os.Unsetenv(capabilityEnv)
		}
	})

	tokensFor := func(capability string) []string {
		GinkgoHelper()
		Expect(os.Setenv(capabilityEnv, capability)).To(Succeed())
		return (&SystemState{}).EnginePreferenceTokens()
	}

	It("puts vLLM ahead of llama.cpp on nvidia", func() {
		// The rule the whole engine table exists to express. A build tag list
		// here would match no engine name at all and this would read empty.
		Expect(tokensFor("nvidia-cuda-12")).To(Equal([]string{"vllm", "sglang", "llama-cpp"}))
	})

	It("puts MLX ahead of llama.cpp on apple silicon", func() {
		Expect(tokensFor(metal)).To(Equal([]string{"mlx", "llama-cpp"}))
	})

	It("gives the GPU serving engines to every vendor that has builds of them", func() {
		Expect(tokensFor(AMD)).To(Equal([]string{"vllm", "sglang", "llama-cpp"}))
		Expect(tokensFor(Intel)).To(Equal([]string{"vllm", "sglang", "llama-cpp"}))
	})

	It("prefers llama.cpp on vulkan, the only engine with a vulkan build", func() {
		Expect(tokensFor(vulkan)).To(Equal([]string{"llama-cpp"}))
	})

	It("names only engines, never a build tag", func() {
		// The mirror of the lock on BackendPreferenceTokens. A build tag here
		// matches no gallery `backend:` value and silently disables ranking.
		buildTags := []string{"cuda", "rocm", "hip", "sycl", "vulkan", "metal", "cpu", "darwin-x86"}
		for _, capability := range []string{"nvidia", AMD, Intel, metal, vulkan, defaultCapability, darwinX86} {
			Expect(tokensFor(capability)).ToNot(ContainElements(buildTags),
				"capability %q leaked a build tag into the engine table", capability)
		}
	})

	It("prefers llama.cpp on a host with no usable accelerator", func() {
		// Nothing filters a GPU serving engine out here: IsBackendCompatible
		// keys on the engine name, and "vllm" carries no hardware token. Without
		// this rule a larger vLLM build would win a CPU-only host on size.
		Expect(tokensFor(defaultCapability)).To(Equal([]string{"llama-cpp", "vllm", "sglang"}))
	})

	It("prefers llama.cpp on an intel mac, where nothing accelerates", func() {
		Expect(tokensFor(darwinX86)).To(Equal([]string{"llama-cpp", "vllm", "sglang"}))
	})

	It("ranks the GPU serving engines below llama.cpp rather than leaving them tied", func() {
		// Enumerating them is what stops download size deciding between vLLM and
		// SGLang once llama.cpp is out of the running.
		for _, capability := range []string{defaultCapability, darwinX86} {
			tokens := tokensFor(capability)
			Expect(tokens).To(HaveLen(3), "capability %q", capability)
			Expect(tokens[0]).To(Equal("llama-cpp"), "capability %q", capability)
		}
	})

	It("leaves a capability nobody has taught the table about empty rather than guessing", func() {
		// Empty is read by the variant ranker as "order by size alone", which is
		// the behaviour that predates preference and is always safe. Only a
		// forced, unrecognised capability lands here now.
		Expect(tokensFor("some-future-accelerator")).To(BeEmpty())
	})

	It("matches a capability by prefix so a refined one keeps its engine order", func() {
		Expect(tokensFor("nvidia-l4t-cuda-13")).To(Equal(tokensFor("nvidia")))
	})

	It("hands out a copy so one caller cannot corrupt another's lookup", func() {
		first := tokensFor("nvidia")
		first[0] = "clobbered"
		Expect(tokensFor("nvidia")[0]).To(Equal("vllm"))
	})
})

var _ = Describe("CapabilityFilterDisabled", func() {
	var origEnv string

	BeforeEach(func() {
		origEnv = os.Getenv(capabilityEnv)
	})

	AfterEach(func() {
		if origEnv != "" {
			os.Setenv(capabilityEnv, origEnv)
		} else {
			os.Unsetenv(capabilityEnv)
		}
	})

	It("returns true when capability is set to disable", func() {
		os.Setenv(capabilityEnv, "disable")
		s := &SystemState{}
		Expect(s.CapabilityFilterDisabled()).To(BeTrue())
	})

	It("returns false when capability is not set to disable", func() {
		os.Setenv(capabilityEnv, "nvidia")
		s := &SystemState{}
		Expect(s.CapabilityFilterDisabled()).To(BeFalse())
	})

	It("makes IsBackendCompatible return true for all backends when disabled", func() {
		os.Setenv(capabilityEnv, "disable")
		s := &SystemState{}
		Expect(s.IsBackendCompatible("cuda12-whisperx", "quay.io/nvidia-cuda-12")).To(BeTrue())
		Expect(s.IsBackendCompatible("metal-whisperx", "quay.io/metal-darwin")).To(BeTrue())
		Expect(s.IsBackendCompatible("intel-whisperx", "quay.io/intel-sycl")).To(BeTrue())
		Expect(s.IsBackendCompatible("cpu-whisperx", "quay.io/cpu")).To(BeTrue())
	})
})

var _ = Describe("DetectedCapability", func() {
	var previous string

	BeforeEach(func() {
		previous = os.Getenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY")
	})

	AfterEach(func() {
		Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", previous)).To(Succeed())
	})

	It("returns the forced capability verbatim without map fallback", func() {
		Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", "nvidia-cuda-12")).To(Succeed())
		state := &SystemState{}
		Expect(state.DetectedCapability()).To(Equal("nvidia-cuda-12"))
	})

	It("does not fall back to default when the capability is unusual", func() {
		Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", "metal")).To(Succeed())
		state := &SystemState{}
		Expect(state.DetectedCapability()).To(Equal("metal"))
		Expect(state.DetectedCapability()).NotTo(Equal("default"))
	})
})

var _ = Describe("ServingFeaturePreferenceTokens", func() {
	It("puts dflash ahead of mtp", func() {
		// The rule the serving feature table exists to express. Both are ways
		// of serving the same weights faster; only this order separates them.
		Expect(ServingFeaturePreferenceTokens()).To(Equal([]string{"dflash", "mtp"}))
	})

	It("carries neither a build tag nor an engine name", func() {
		// The third vocabulary, and the same merge hazard as the other two. A
		// build tag or an engine name here would be matched against gallery
		// entry names, where it means nothing.
		Expect(ServingFeaturePreferenceTokens()).ToNot(ContainElements(
			"cuda", "rocm", "hip", "sycl", "metal", "vulkan", "cpu", "darwin-x86",
			"vllm", "sglang", "llama-cpp", "mlx",
		))
	})

	It("does not depend on the host capability", func() {
		// Deliberately not a SystemState method: no hardware prefers the plain
		// build over an equivalent faster one, so there is no host-shaped
		// ordering to express here.
		previous := os.Getenv(capabilityEnv)
		defer func() {
			if previous != "" {
				os.Setenv(capabilityEnv, previous)
			} else {
				os.Unsetenv(capabilityEnv)
			}
		}()

		Expect(os.Setenv(capabilityEnv, "nvidia-cuda-12")).To(Succeed())
		onNvidia := ServingFeaturePreferenceTokens()
		Expect(os.Setenv(capabilityEnv, metal)).To(Succeed())
		Expect(ServingFeaturePreferenceTokens()).To(Equal(onNvidia))
	})

	It("hands out a copy so one caller cannot corrupt another's lookup", func() {
		first := ServingFeaturePreferenceTokens()
		first[0] = "clobbered"
		Expect(ServingFeaturePreferenceTokens()[0]).To(Equal("dflash"))
	})
})
