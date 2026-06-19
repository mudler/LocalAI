package nodes

import (
	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("applyNodeHardwareDefaults", func() {
	It("raises a managed default batch on a Blackwell node", func() {
		opts := &pb.ModelOptions{NBatch: config.DefaultPhysicalBatch}
		applyNodeHardwareDefaults(opts, &BackendNode{GPUComputeCapability: "12.1"})
		Expect(opts.NBatch).To(BeEquivalentTo(config.BlackwellPhysicalBatch))
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

	It("no-ops on nil inputs", func() {
		Expect(func() { applyNodeHardwareDefaults(nil, nil) }).ToNot(Panic())
	})
})
