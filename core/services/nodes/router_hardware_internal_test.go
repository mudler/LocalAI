package nodes

import (
	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("applyNodeHardwareDefaults", func() {
	It("raises a managed default batch on a Blackwell node with headroom", func() {
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch, BatchManaged: true, ContextSize: 8192}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1", TotalVRAM: 119 << 30}, "llama-cpp")
		Expect(opts.NBatch).To(BeEquivalentTo(config.BlackwellPhysicalBatch))
	})

	It("keeps the default batch when a large context would overflow the node", func() {
		// Regression guard for issue #10485 on the distributed path.
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch, BatchManaged: true, ContextSize: 204800}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.0", TotalVRAM: 16 << 30}, "llama-cpp")
		Expect(opts.NBatch).To(BeEquivalentTo(config.DefaultPhysicalBatch))
	})

	It("resets a Blackwell guess on a non-Blackwell node", func() {
		// frontend (Blackwell) guessed high, but the selected node is not Blackwell
		opts := &pb.ModelOptions{NBatch: config.BlackwellPhysicalBatch, BatchManaged: true}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "9.0"}, "llama-cpp")
		Expect(opts.NBatch).To(BeEquivalentTo(config.DefaultPhysicalBatch))
	})

	It("never overrides an explicit batch that equals a managed value", func() {
		// batch: 2048 set by the user must reach llama.cpp as n_batch 2048 on a
		// node that would otherwise default to 512.
		opts := &pb.ModelOptions{NBatch: config.BlackwellPhysicalBatch}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "9.0"}, "llama-cpp")
		Expect(opts.NBatch).To(BeEquivalentTo(config.BlackwellPhysicalBatch))
	})

	It("never overrides an explicit (non-managed) batch", func() {
		opts := &pb.ModelOptions{NBatch: 1024}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1"}, "llama-cpp")
		Expect(opts.NBatch).To(BeEquivalentTo(int32(1024)))
	})

	It("counts the recurrent state of a hybrid model on the selected node", func() {
		saved := recurrentStateOf
		DeferCleanup(func() { recurrentStateOf = saved })
		recurrentStateOf = func(path string) uint64 {
			Expect(path).To(Equal("/models/hybrid.gguf"))
			return 1200 << 20 // a large hybrid: 1.2 GiB per slot
		}
		opts := &pb.ModelOptions{ModelFile: "/models/hybrid.gguf", ContextSize: 4096}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "8.9", TotalVRAM: 16 << 30, TotalRAM: 32 << 30}, "llama-cpp")
		// 4 slots of state do not fit a quarter of 16 GiB: two do. The checkpoints of
		// both slots get a quarter of the node's 32 GiB: 8 GiB / (2 x 1.2 GiB) is 3,
		// raised to the floor of 4.
		Expect(opts.Options).To(Equal([]string{"parallel:2", "n_ctx_checkpoints:4"}))
	})

	It("leaves a model without a recurrent state as before", func() {
		saved := recurrentStateOf
		DeferCleanup(func() { recurrentStateOf = saved })
		recurrentStateOf = func(string) uint64 { return 0 }
		opts := &pb.ModelOptions{ModelFile: "/models/dense.gguf", ContextSize: 4096}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "8.9", TotalVRAM: 16 << 30, TotalRAM: 32 << 30}, "llama-cpp")
		Expect(opts.Options).To(Equal([]string{"parallel:4"}))
	})

	It("adds a VRAM-scaled parallel option for the selected node", func() {
		// frontend may have had no GPU (no parallel option); the node has a big GPU
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1", TotalVRAM: 119 << 30}, "llama-cpp")
		Expect(opts.Options).To(ContainElement("parallel:8"))
	})

	It("pins one slot when a large context already fills the node device", func() {
		// Regression guard for issue #10485: a 16 GiB node with a ~200k context
		// is a tight single-model fit — the slot scratch would tip it into OOM.
		// parallel:1 keeps the backend's paged serving default (8 slots) out.
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch, BatchManaged: true, ContextSize: 204800}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.0", TotalVRAM: 16 << 30}, "llama-cpp")
		Expect(opts.Options).To(Equal([]string{"parallel:1"}))
	})

	It("never overrides an explicit parallel option on the node path", func() {
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch, Options: []string{"parallel:2"}}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1", TotalVRAM: 119 << 30}, "llama-cpp")
		Expect(opts.Options).To(Equal([]string{"parallel:2"}))
	})

	It("adds no parallel option for a non-llama backend on the node path", func() {
		// parallel is a llama.cpp option string; a backend that strictly validates
		// options (e.g. longcat-video) rejects it. The node-tuning path must gate
		// it by backend just like the single-host config path does.
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1", TotalVRAM: 119 << 30}, "longcat-video")
		Expect(opts.Options).ToNot(ContainElement(ContainSubstring("parallel")))
	})

	It("no-ops on nil inputs", func() {
		Expect(func() { applyNodeHardwareDefaults(nil, nil, "llama-cpp") }).ToNot(Panic())
	})
})
