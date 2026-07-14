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
