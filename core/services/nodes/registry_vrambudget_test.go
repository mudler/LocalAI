package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

var _ = Describe("Node VRAM budget", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
	)

	// gb is 1000-based to match vrambudget's decimal "GB" suffix.
	const gb = uint64(1000 * 1000 * 1000)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// seedHealthyNode registers an auto-approved backend node with raw total and
	// available VRAM and returns its ID.
	seedHealthyNode := func(ctx context.Context, name string, total, avail uint64) string {
		node := &BackendNode{
			Name:          name,
			NodeType:      NodeTypeBackend,
			Address:       "10.0.0.1:50051",
			TotalVRAM:     total,
			AvailableVRAM: avail,
		}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		return node.ID
	}

	It("resolves a percentage budget against the node's raw total VRAM", func() {
		Expect(registry.ResolveVRAMBudgetBytesForTest("80%", 10*gb)).To(Equal(8 * gb))
		Expect(registry.ResolveVRAMBudgetBytesForTest("12GB", 10*gb)).To(Equal(10 * gb)) // clamped to physical
		Expect(registry.ResolveVRAMBudgetBytesForTest("", 10*gb)).To(Equal(uint64(0)))
	})

	It("caps stored available_vram when an admin sets a budget", func(ctx SpecContext) {
		id := seedHealthyNode(ctx, "worker-budget-1", 16*gb, 16*gb)
		Expect(registry.UpdateVRAMBudget(ctx, id, "50%")).To(Succeed())

		node, err := registry.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(node.TotalVRAM).To(Equal(16 * gb)) // raw preserved
		Expect(node.VRAMBudgetBytes).To(Equal(8 * gb))
		Expect(node.AvailableVRAM).To(Equal(8 * gb)) // capped
		Expect(node.VRAMBudgetManuallySet).To(BeTrue())
	})

	It("re-caps available_vram on heartbeat against the stored ceiling", func(ctx SpecContext) {
		id := seedHealthyNode(ctx, "worker-budget-2", 16*gb, 16*gb)
		Expect(registry.UpdateVRAMBudget(ctx, id, "8GB")).To(Succeed())

		avail := 15 * gb
		Expect(registry.Heartbeat(ctx, id, &HeartbeatUpdate{AvailableVRAM: &avail})).To(Succeed())
		node, err := registry.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(node.AvailableVRAM).To(Equal(8 * gb)) // reported 15GB capped to 8GB budget
	})

	It("preserves an admin override across worker re-registration", func(ctx SpecContext) {
		id := seedHealthyNode(ctx, "worker-budget-3", 16*gb, 16*gb)
		Expect(registry.UpdateVRAMBudget(ctx, id, "50%")).To(Succeed())

		// Worker re-registers reporting full available VRAM and no budget.
		reReg := &BackendNode{
			Name:          "worker-budget-3",
			NodeType:      NodeTypeBackend,
			Address:       "10.0.0.1:50051",
			TotalVRAM:     16 * gb,
			AvailableVRAM: 16 * gb,
		}
		Expect(registry.Register(ctx, reReg, true)).To(Succeed())

		node, err := registry.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(node.VRAMBudgetManuallySet).To(BeTrue())
		Expect(node.VRAMBudget).To(Equal("50%"))
		Expect(node.VRAMBudgetBytes).To(Equal(8 * gb))
		Expect(node.AvailableVRAM).To(Equal(8 * gb)) // re-capped despite worker reporting 16GB
	})

	It("clears the cap when the budget is reset", func(ctx SpecContext) {
		id := seedHealthyNode(ctx, "worker-budget-4", 16*gb, 16*gb)
		Expect(registry.UpdateVRAMBudget(ctx, id, "50%")).To(Succeed())
		Expect(registry.ResetVRAMBudget(ctx, id)).To(Succeed())
		node, err := registry.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(node.VRAMBudgetBytes).To(Equal(uint64(0)))
		Expect(node.VRAMBudgetManuallySet).To(BeFalse())
	})

	// reregister re-registers an existing node by name, mirroring what the worker
	// does on restart (raw total/available, and whatever budget it currently
	// reports via LOCALAI_VRAM_BUDGET).
	reregister := func(ctx context.Context, name, budget string, total, avail uint64) {
		reReg := &BackendNode{
			Name:          name,
			NodeType:      NodeTypeBackend,
			Address:       "10.0.0.1:50051",
			TotalVRAM:     total,
			AvailableVRAM: avail,
			VRAMBudget:    budget,
		}
		Expect(registry.Register(ctx, reReg, true)).To(Succeed())
	}

	It("clears a worker-reported budget when the worker re-registers without one", func(ctx SpecContext) {
		// Worker first reports LOCALAI_VRAM_BUDGET=80%.
		node := &BackendNode{
			Name:          "worker-clear-1",
			NodeType:      NodeTypeBackend,
			Address:       "10.0.0.1:50051",
			TotalVRAM:     16 * gb,
			AvailableVRAM: 16 * gb,
			VRAMBudget:    "80%",
		}
		Expect(registry.Register(ctx, node, true)).To(Succeed())

		got, err := registry.Get(ctx, node.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.VRAMBudget).To(Equal("80%"))
		Expect(got.VRAMBudgetBytes).To(BeNumerically(">", uint64(0)))
		Expect(got.VRAMBudgetBytes).To(Equal(uint64(12.8 * float64(gb)))) // 80% of 16GB
		Expect(got.AvailableVRAM).To(Equal(got.VRAMBudgetBytes))          // capped

		// Operator removes LOCALAI_VRAM_BUDGET and restarts the worker: it now
		// re-registers with an empty budget and full raw available. The stale
		// 80% cap MUST be cleared, not preserved (struct Updates zero-skip bug).
		reregister(ctx, "worker-clear-1", "", 16*gb, 16*gb)

		got, err = registry.Get(ctx, node.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.VRAMBudget).To(Equal(""))
		Expect(got.VRAMBudgetBytes).To(Equal(uint64(0)))
		Expect(got.AvailableVRAM).To(Equal(16 * gb)) // back to raw reported
	})

	It("keeps an admin override when a worker re-registers without a budget", func(ctx SpecContext) {
		// Regression guard for the force-clear: it must not clobber a sticky
		// admin-set budget just because the worker stopped reporting one.
		id := seedHealthyNode(ctx, "worker-clear-2", 16*gb, 16*gb)
		Expect(registry.UpdateVRAMBudget(ctx, id, "50%")).To(Succeed())

		reregister(ctx, "worker-clear-2", "", 16*gb, 16*gb)

		node, err := registry.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(node.VRAMBudgetManuallySet).To(BeTrue())
		Expect(node.VRAMBudget).To(Equal("50%"))
		Expect(node.VRAMBudgetBytes).To(Equal(8 * gb))
		Expect(node.AvailableVRAM).To(Equal(8 * gb)) // still capped by admin budget
	})
})
