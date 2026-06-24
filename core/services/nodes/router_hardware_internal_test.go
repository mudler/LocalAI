package nodes

import (
	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("applyNodeHardwareDefaults", func() {
	It("raises a managed default batch on a Blackwell node with headroom", func() {
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch, ContextSize: 8192}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1", TotalVRAM: 119 << 30})
		Expect(opts.NBatch).To(BeEquivalentTo(config.BlackwellPhysicalBatch))
	})

	It("keeps the default batch when a large context would overflow the node", func() {
		// Regression guard for issue #10485 on the distributed path.
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch, ContextSize: 204800}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.0", TotalVRAM: 16 << 30})
		Expect(opts.NBatch).To(BeEquivalentTo(config.DefaultPhysicalBatch))
	})

	It("resets a Blackwell guess on a non-Blackwell node", func() {
		// frontend (Blackwell) guessed high, but the selected node is not Blackwell
		opts := &pb.ModelOptions{NBatch: config.BlackwellPhysicalBatch}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "9.0"})
		Expect(opts.NBatch).To(BeEquivalentTo(config.DefaultPhysicalBatch))
	})

	It("never overrides an explicit (non-managed) batch", func() {
		opts := &pb.ModelOptions{NBatch: 1024}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1"})
		Expect(opts.NBatch).To(BeEquivalentTo(int32(1024)))
	})

	It("adds a VRAM-scaled parallel option for the selected node", func() {
		// frontend may have had no GPU (no parallel option); the node has a big GPU
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1", TotalVRAM: 119 << 30})
		Expect(opts.Options).To(ContainElement("parallel:8"))
	})

	It("never overrides an explicit parallel option on the node path", func() {
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch, Options: []string{"parallel:2"}}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1", TotalVRAM: 119 << 30})
		Expect(opts.Options).To(Equal([]string{"parallel:2"}))
	})

	It("no-ops on nil inputs", func() {
		Expect(func() { applyNodeHardwareDefaults(nil, nil) }).ToNot(Panic())
	})
})
