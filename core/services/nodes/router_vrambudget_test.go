package nodes

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("applyNodeHardwareDefaults with VRAM budget", func() {
	const gb = uint64(1000 * 1000 * 1000)

	It("uses the capped ceiling, not raw TotalVRAM, for batch/parallel gating", func() {
		// A Blackwell node whose RAW VRAM has ample headroom to keep the raised
		// physical batch, but whose operator budget is far too small for it. The
		// batch/parallel heuristics must gate on the budgeted ceiling, not the
		// physical card, or a budgeted-tiny node would still get OOM-prone
		// throughput defaults meant for the full device.
		//
		// The context is deliberately large: the compute-buffer headroom guard
		// (PhysicalBatchForContext) scales the extra scratch by n_ctx, so at a
		// small context even 2GB clears the guard. At 32768 the raised batch's
		// scratch fits comfortably in 64GB (raw keeps 2048) but overflows a
		// quarter of a 2GB budget (budget drops it to the conservative default).
		const largeCtx = int32(32768)
		node := &BackendNode{
			GPUVendor:            "nvidia",
			GPUComputeCapability: "12.1",
			TotalVRAM:            64 * gb,
			VRAMBudgetBytes:      2 * gb, // tiny operator budget
		}
		opts := &pb.ModelOptions{NBatch: int32(config.BlackwellPhysicalBatch), ContextSize: largeCtx}

		applyNodeHardwareDefaults(opts, node, "llama-cpp")

		// With only 2GB budgeted the raised 2048 batch must not survive.
		Expect(int(opts.NBatch)).To(BeNumerically("<", config.BlackwellPhysicalBatch))
	})
})
