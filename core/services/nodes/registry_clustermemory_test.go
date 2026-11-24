package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// The model gallery on a GPU-less controller sizes every "does this fit"
// verdict against what this reports, so the node it picks decides which models
// admins are told they can run.
var _ = Describe("NodeRegistry HealthyNodeMemory", func() {
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

	It("reports the VRAM of the single GPU worker", func() {
		register(&BackendNode{
			Name: "gpu-worker", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalVRAM: 24_000_000_000, TotalRAM: 64_000_000_000, GPUVendor: "nvidia",
		})

		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).ToNot(BeNil())
		Expect(mem.TotalMemory).To(Equal(uint64(24_000_000_000)))
		Expect(mem.NodeName).To(Equal("gpu-worker"))
		Expect(mem.IsGPU).To(BeTrue())
		Expect(mem.NodeCount).To(Equal(1))
	})

	// A model has to load on ONE node, so the biggest single node is the
	// budget. Summing the fleet would promise a 40GB model fits on a cluster of
	// four 16GB cards that can never hold it.
	It("picks the largest single node rather than the fleet total", func() {
		register(&BackendNode{
			Name: "small", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalVRAM: 16_000_000_000, GPUVendor: "nvidia",
		})
		register(&BackendNode{
			Name: "large", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051",
			TotalVRAM: 48_000_000_000, GPUVendor: "nvidia",
		})
		register(&BackendNode{
			Name: "medium", NodeType: NodeTypeBackend, Address: "10.0.0.3:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia",
		})

		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).ToNot(BeNil())
		Expect(mem.TotalMemory).To(Equal(uint64(48_000_000_000)))
		Expect(mem.NodeName).To(Equal("large"))
		Expect(mem.NodeCount).To(Equal(3))
	})

	// A CPU worker runs models out of system RAM, exactly as a single-node
	// LocalAI does when it finds no GPU.
	It("falls back to system RAM for a worker with no GPU", func() {
		register(&BackendNode{
			Name: "cpu-worker", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalRAM: 128_000_000_000,
		})

		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).ToNot(BeNil())
		Expect(mem.TotalMemory).To(Equal(uint64(128_000_000_000)))
		Expect(mem.NodeName).To(Equal("cpu-worker"))
		Expect(mem.IsGPU).To(BeFalse())
	})

	// A 24GB card beats a 512GB CPU box for anything a user would actually
	// serve, so a GPU node wins even when a CPU node reports more bytes.
	It("prefers a GPU node over a CPU node holding more system RAM", func() {
		register(&BackendNode{
			Name: "fat-cpu", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalRAM: 512_000_000_000,
		})
		register(&BackendNode{
			Name: "gpu", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051",
			TotalVRAM: 24_000_000_000, GPUVendor: "nvidia",
		})

		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).ToNot(BeNil())
		Expect(mem.NodeName).To(Equal("gpu"))
		Expect(mem.IsGPU).To(BeTrue())
		Expect(mem.TotalMemory).To(Equal(uint64(24_000_000_000)))
	})

	// The same predicate the scheduler places against: an unhealthy worker
	// cannot take a load, so its hardware must not size the catalog either.
	It("ignores unhealthy nodes", func() {
		register(&BackendNode{
			Name: "healthy", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalVRAM: 16_000_000_000, GPUVendor: "nvidia",
		})
		register(&BackendNode{
			Name: "offline", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051",
			TotalVRAM: 80_000_000_000, GPUVendor: "nvidia",
		})
		offline, err := registry.GetByName(context.Background(), "offline")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.MarkUnhealthy(context.Background(), offline.ID)).To(Succeed())

		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).ToNot(BeNil())
		Expect(mem.NodeName).To(Equal("healthy"))
		Expect(mem.NodeCount).To(Equal(1))
	})

	// The scheduler refuses a load that exceeds an operator-set budget, so a
	// verdict sized against raw VRAM would promise a fit the cluster rejects.
	It("respects an operator-set VRAM budget", func() {
		register(&BackendNode{
			Name: "capped", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
			TotalVRAM: 48_000_000_000, GPUVendor: "nvidia",
			VRAMBudget: "24GB", VRAMBudgetManuallySet: true,
		})

		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).ToNot(BeNil())
		Expect(mem.TotalMemory).To(Equal(uint64(24_000_000_000)))
	})

	// Nothing to size against is not an error: the caller degrades to the
	// controller's own memory rather than blanking the catalog.
	It("returns no reading when the cluster has no healthy backend node", func() {
		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).To(BeNil())
	})

	// A worker that reports neither VRAM nor RAM tells us nothing, and a zero
	// budget would mark every model as too large.
	It("returns no reading when every healthy node reports zero memory", func() {
		register(&BackendNode{
			Name: "silent", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051",
		})

		mem, err := registry.HealthyNodeMemory(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(mem).To(BeNil())
	})
})
