package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config/harnessctx"
)

// These specs cover the plan a client reads before it installs a model. The
// window comes from the code the loader runs; the specs check that the plan
// follows the loader and adds a verdict.
var _ = Describe("ModelProfile.PlanContext", func() {
	const gib = uint64(1) << 30

	// A model of 5.5 GiB of weights and a KV cache of 128 KiB per token, for
	// the VRAM and the system-memory figures alike.
	profile := func(weights, kvPerToken uint64) *ModelProfile {
		h := ggufHeader{Architecture: "llama", TrainedContext: 131072, footprints: map[int]uint64{}, ramFootprints: map[int]uint64{}}
		for _, c := range append([]int{harnessctx.RecommendedContext}, contextFitCandidates...) {
			h.footprints[c] = weights + uint64(c)*kvPerToken
			h.ramFootprints[c] = weights + uint64(c)*kvPerToken
		}
		return &ModelProfile{h: h}
	}
	model8B := profile(5*gib+gib/2, 128<<10)

	It("gives the window the loader picks, whatever the machine running the plan", func() {
		origVRAM, origRAM := perDeviceVRAM, systemTotalRAM
		DeferCleanup(func() { perDeviceVRAM, systemTotalRAM = origVRAM, origRAM })
		perDeviceVRAM = func() uint64 { return 0 }
		systemTotalRAM = func() uint64 { return 2 * gib }

		for _, hw := range []HostMemory{
			{PerDeviceVRAM: 24 * gib, SystemRAM: 32 * gib},
			{PerDeviceVRAM: 8 * gib, SystemRAM: 16 * gib},
			{SystemRAM: 8 * gib},
			{SystemRAM: 64 * gib},
		} {
			want := autoContextSizeFor(hw.PerDeviceVRAM, hw.SystemRAM, model8B.h.footprintAt, model8B.h.ramFootprintAt, 131072, harnessctx.RecommendedContext)
			Expect(PlanContext(model8B, hw).ContextSize).To(Equal(want))
		}
	})

	DescribeTable("the verdict",
		func(hw HostMemory, window int, fit ContextFit, reasons ...string) {
			plan := PlanContext(model8B, hw)
			Expect(plan.ContextSize).To(Equal(window))
			Expect(plan.Fit).To(Equal(fit))
			Expect(plan.Reasons).To(ConsistOf(reasons))
			Expect(plan.MemoryBytes).ToNot(BeZero())
		},
		Entry("a roomy card", HostMemory{PerDeviceVRAM: 24 * gib}, 32768, FitFits),
		Entry("a card with room for a smaller window", HostMemory{PerDeviceVRAM: 11 * gib}, 24576, FitFits, ReasonWindowReduced),
		Entry("a card that holds only the chat minimum", HostMemory{PerDeviceVRAM: 10 * gib}, 16384, FitTight, ReasonWindowReduced),
		Entry("a card too small for the chat", HostMemory{PerDeviceVRAM: 8 * gib}, 8192, FitNone, ReasonBelowChatMinimum),
		Entry("a card at the edge of the weights", HostMemory{PerDeviceVRAM: 6 * gib}, 4096, FitNone, ReasonBelowChatMinimum),
		Entry("a card that cannot hold the weights", HostMemory{PerDeviceVRAM: 4 * gib}, 4096, FitNone, ReasonExceedsMemory),
		Entry("a host with plenty of memory and no GPU", HostMemory{SystemRAM: 32 * gib}, 32768, FitFits),
		Entry("a host that holds the chat minimum over its budget", HostMemory{SystemRAM: 8 * gib}, 16384, FitTight, ReasonLittleHeadroom, ReasonWindowReduced),
		Entry("a host with too little memory", HostMemory{SystemRAM: 5 * gib}, 4096, FitNone, ReasonExceedsMemory),
	)

	It("plans from any MemoryModel, not only a GGUF header", func() {
		plan := PlanContext(tableModel{weights: 4 * gib, perToken: 64 << 10}, HostMemory{PerDeviceVRAM: 16 * gib})
		Expect(plan.ContextSize).To(Equal(32768))
		Expect(plan.Fit).To(Equal(FitFits))
	})

	It("does not claim a verdict when the host memory is unknown", func() {
		plan := PlanContext(model8B, HostMemory{})
		Expect(plan.Fit).To(Equal(FitUnknown))
		Expect(plan.Reasons).To(ConsistOf(ReasonHardwareUnknown))
	})

	It("does not claim a verdict for a file the estimator could not read", func() {
		plan := PlanContext(&ModelProfile{}, HostMemory{PerDeviceVRAM: 24 * gib})
		Expect(plan.Fit).To(Equal(FitUnknown))
		Expect(plan.ContextSize).To(Equal(DefaultContextSize))
	})
})

// tableModel is a MemoryModel from two numbers, as a table of published
// figures would give.
type tableModel struct{ weights, perToken uint64 }

func (t tableModel) WeightsBytes() uint64 { return t.weights }
func (t tableModel) TrainedContext() int  { return 131072 }
func (t tableModel) Architecture() string { return "llama" }
func (t tableModel) VRAMAt(w int) uint64  { return t.weights + uint64(w)*t.perToken }
func (t tableModel) RAMAt(w int) uint64   { return t.weights + uint64(w)*t.perToken }
