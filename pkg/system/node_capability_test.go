package system

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewCapabilityState", func() {
	It("reports exactly the supplied capability", func() {
		Expect(NewCapabilityState("nvidia-l4t-cuda-13").DetectedCapability()).To(Equal("nvidia-l4t-cuda-13"))
	})

	It("ignores a forced capability meant for the local host", func() {
		// A controller container image commonly pins its own capability via
		// this env var (or /run/localai/capability). That must never override
		// the capability we are evaluating on a remote worker's behalf.
		orig, had := os.LookupEnv(capabilityEnv)
		Expect(os.Setenv(capabilityEnv, "metal")).To(Succeed())
		DeferCleanup(func() {
			if had {
				Expect(os.Setenv(capabilityEnv, orig)).To(Succeed())
				return
			}
			Expect(os.Unsetenv(capabilityEnv)).To(Succeed())
		})

		Expect(NewCapabilityState("nvidia-cuda-13").DetectedCapability()).To(Equal("nvidia-cuda-13"))
	})

	It("applies the supplied state options", func() {
		state := NewCapabilityState("nvidia", WithBackendPath("/some/backends"))
		Expect(state.Backend.BackendsPath).To(Equal("/some/backends"))
	})

	It("honors the disable escape hatch when that is the pinned capability", func() {
		Expect(NewCapabilityState(disableCapability).CapabilityFilterDisabled()).To(BeTrue())
	})
})

var _ = Describe("CapabilityFromGPU", func() {
	const eightGB = 8 * 1024 * 1024 * 1024
	const twoGB = 2 * 1024 * 1024 * 1024

	It("returns the vendor for a GPU with enough VRAM", func() {
		Expect(CapabilityFromGPU(Nvidia, eightGB)).To(Equal(Nvidia))
		Expect(CapabilityFromGPU(AMD, eightGB)).To(Equal(AMD))
	})

	It("returns default when no GPU is reported", func() {
		Expect(CapabilityFromGPU("", eightGB)).To(Equal(defaultCapability))
	})

	It("returns default when VRAM is below the usable threshold", func() {
		Expect(CapabilityFromGPU(Nvidia, twoGB)).To(Equal(defaultCapability))
	})
})
