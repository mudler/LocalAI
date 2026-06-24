package gallery

import (
	"os"

	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/xsysinfo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("availableModelMemory", func() {
	// hostRAM is the figure the fallback resolves to. A host that cannot report
	// its own RAM cannot exercise the fallback at all, so those specs skip
	// rather than assert on a number that is legitimately unavailable.
	hostRAM := func() uint64 {
		ram, err := xsysinfo.GetSystemRAMInfo()
		if err != nil || ram == nil || ram.Total == 0 {
			Skip("this host does not report system RAM; the RAM fallback cannot be exercised here")
		}
		return ram.Total
	}

	// stateWithCapability forces DetectedCapability for one spec. The state is
	// built fresh so nothing is served from the capability cache.
	stateWithCapability := func(capability string, vram uint64) *system.SystemState {
		previous, had := os.LookupEnv("LOCALAI_FORCE_META_BACKEND_CAPABILITY")
		Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", capability)).To(Succeed())
		DeferCleanup(func() {
			if had {
				Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", previous)).To(Succeed())
				return
			}
			Expect(os.Unsetenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY")).To(Succeed())
		})

		s := &system.SystemState{}
		s.VRAM = vram
		Expect(s.DetectedCapability()).To(Equal(capability), "the capability override did not take")
		return s
	}

	// A unified-memory host reports a GPU capability but no discrete VRAM pool.
	// Apple Silicon is the case that matters: metal is reported unconditionally
	// on arm64 macs while TotalAvailableVRAM finds nothing to add up. Reading
	// that zero as the budget drops every sized variant and strands the host on
	// the base build however much memory it has, which is what turned the macOS
	// runner red.
	It("falls back to system RAM when a detected GPU reports no VRAM", func() {
		ram := hostRAM()

		got := availableModelMemory(stateWithCapability("metal", 0))

		Expect(got).ToNot(BeZero(), "a unified-memory host was given a zero budget; every sized variant would be dropped")
		Expect(got).To(Equal(ram))
	})

	// A discrete GPU reports real VRAM, and that stays the budget: the model
	// lives on the card, so system RAM is not what bounds it.
	It("prefers VRAM when the GPU reports it", func() {
		const vram = uint64(24) << 30

		Expect(availableModelMemory(stateWithCapability("nvidia-cuda-12", vram))).To(Equal(vram))
	})

	// With no GPU at all the model lives in system RAM.
	It("uses system RAM when no GPU is detected", func() {
		ram := hostRAM()

		Expect(availableModelMemory(stateWithCapability("default", 0))).To(Equal(ram))
	})
})
