package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// Backend discovery on a GPU-less controller unions these capabilities, so the
// set this returns decides which GPU-only backends admins can see at all.
var _ = Describe("NodeRegistry HealthyBackendCapabilities", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	register := func(node *BackendNode) {
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
	}

	It("returns the worker-reported capability", func() {
		register(&BackendNode{
			Name: "gpu-worker", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia", Capability: "nvidia-cuda-13",
		})

		caps, err := registry.HealthyBackendCapabilities(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(caps).To(ConsistOf("nvidia-cuda-13"))
	})

	It("falls back to the GPU vendor for workers that report no capability", func() {
		register(&BackendNode{
			Name: "legacy-worker", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia",
		})

		caps, err := registry.HealthyBackendCapabilities(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(caps).To(ConsistOf("nvidia"))
	})

	It("reports default for a legacy worker whose VRAM is below the GPU threshold", func() {
		register(&BackendNode{
			Name: "tiny-worker", NodeType: NodeTypeBackend, Address: "10.0.0.3:50051",
			TotalVRAM: 2_000_000_000, GPUVendor: "nvidia",
		})

		caps, err := registry.HealthyBackendCapabilities(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(caps).To(ConsistOf("default"))
	})

	It("deduplicates capabilities across a homogeneous fleet", func() {
		register(&BackendNode{
			Name: "gpu-a", NodeType: NodeTypeBackend, Address: "10.0.0.4:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia", Capability: "nvidia-cuda-12",
		})
		register(&BackendNode{
			Name: "gpu-b", NodeType: NodeTypeBackend, Address: "10.0.0.5:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia", Capability: "nvidia-cuda-12",
		})

		caps, err := registry.HealthyBackendCapabilities(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(caps).To(ConsistOf("nvidia-cuda-12"))
	})

	It("collects every distinct capability in a heterogeneous fleet", func() {
		register(&BackendNode{
			Name: "nvidia-worker", NodeType: NodeTypeBackend, Address: "10.0.0.6:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia", Capability: "nvidia-cuda-13",
		})
		register(&BackendNode{
			Name: "mac-worker", NodeType: NodeTypeBackend, Address: "10.0.0.7:50051",
			TotalVRAM: 32_000_000_000, Capability: "metal",
		})

		caps, err := registry.HealthyBackendCapabilities(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(caps).To(ConsistOf("nvidia-cuda-13", "metal"))
	})

	It("ignores nodes that are not healthy backend nodes", func() {
		// A pending (unapproved) GPU worker must not advertise hardware the
		// cluster cannot schedule onto yet.
		pending := &BackendNode{
			Name: "pending-gpu", NodeType: NodeTypeBackend, Address: "10.0.0.8:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia", Capability: "nvidia-cuda-13",
		}
		Expect(registry.Register(context.Background(), pending, false)).To(Succeed())

		register(&BackendNode{
			Name: "agent-node", NodeType: NodeTypeAgent, Address: "10.0.0.9:50051",
			Capability: "metal",
		})

		caps, err := registry.HealthyBackendCapabilities(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(caps).To(BeEmpty())
	})

	It("returns nothing when no nodes are registered", func() {
		caps, err := registry.HealthyBackendCapabilities(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(caps).To(BeEmpty())
	})
})
