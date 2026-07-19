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

	It("ranks MLX above metal on apple silicon", func() {
		// MLX is the native accelerated runtime there, so it must outrank a
		// metal-enabled build of the portable engine.
		Expect(tokensFor(metal)).To(Equal([]string{"mlx", "metal", "cpu"}))
	})

	It("keeps the vendor order every other capability already had", func() {
		Expect(tokensFor("nvidia-cuda-12")).To(Equal([]string{"cuda", "vulkan", "cpu"}))
		Expect(tokensFor(AMD)).To(Equal([]string{"rocm", "hip", "vulkan", "cpu"}))
		Expect(tokensFor(Intel)).To(Equal([]string{"sycl", "intel", "cpu"}))
		Expect(tokensFor(darwinX86)).To(Equal([]string{"darwin-x86", "cpu"}))
		Expect(tokensFor(vulkan)).To(Equal([]string{"vulkan", "cpu"}))
	})

	It("degrades an unknown capability to cpu rather than to nothing", func() {
		Expect(tokensFor("some-future-accelerator")).To(Equal([]string{"cpu"}))
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
