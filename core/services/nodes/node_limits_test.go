package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

func ptr[T any](v T) *T { return &v }

var _ = Describe("Node memory policy", func() {
	cluster := MemoryPolicy{MaxActiveBackends: 2, ModelSwap: true, ReclaimerThreshold: 0.95, VRAMBudget: "90%"}

	It("takes the cluster value when the node sets nothing", func() {
		p := cluster.For(&BackendNode{})
		Expect(p.MaxActiveBackends).To(Equal(2))
		Expect(p.ModelSwap).To(BeTrue())
		Expect(p.Sources["max_active_backends"]).To(Equal(LimitSourceCluster))
	})

	It("lets the worker's env win over the cluster, and the admin override over both", func() {
		n := &BackendNode{}
		n.SetWorkerLimits(NodeLimits{MaxActiveBackends: ptr(4), ModelSwap: ptr(false)})
		n.LimitsOverride = encodeLimits(NodeLimits{MaxActiveBackends: ptr(1)})

		p := cluster.For(n)

		Expect(p.MaxActiveBackends).To(Equal(1))
		Expect(p.Sources["max_active_backends"]).To(Equal(LimitSourceOverride))
		Expect(p.ModelSwap).To(BeFalse())
		Expect(p.Sources["model_swap"]).To(Equal(LimitSourceWorker))
		Expect(p.Sources["vram_budget"]).To(Equal(LimitSourceCluster))
	})

	It("reclaims at 0.90 on a unified-memory node unless the cluster states the reclaimer", func() {
		p := cluster.For(&BackendNode{UnifiedMemory: true})
		Expect(p.ReclaimerEnabled).To(BeTrue())
		Expect(p.ReclaimerThreshold).To(Equal(UnifiedMemoryReclaimerThreshold))
		Expect(p.Sources["memory_reclaimer_threshold"]).To(Equal(LimitSourceDefault))

		stated := cluster
		stated.ReclaimerStated = true
		Expect(stated.For(&BackendNode{UnifiedMemory: true}).ReclaimerThreshold).To(Equal(0.95))
		Expect(cluster.For(&BackendNode{}).ReclaimerEnabled).To(BeFalse())
	})

	It("turns the reclaimer on for a node that sets its own threshold", func() {
		off := cluster // the cluster leaves the reclaimer off
		off.ReclaimerStated = true

		worker := &BackendNode{}
		worker.SetWorkerLimits(NodeLimits{MemoryReclaimerThreshold: ptr(0.8)})
		p := off.For(worker)
		Expect(p.ReclaimerEnabled).To(BeTrue())
		Expect(p.ReclaimerThreshold).To(Equal(0.8))
		Expect(p.Sources["memory_reclaimer_threshold"]).To(Equal(LimitSourceWorker))
		Expect(p.Limits().MemoryReclaimerEnabled).To(BeTrue())

		override := &BackendNode{LimitsOverride: encodeLimits(NodeLimits{MemoryReclaimerThreshold: ptr(0.7)})}
		p = off.For(override)
		Expect(p.ReclaimerEnabled).To(BeTrue())
		Expect(p.ReclaimerThreshold).To(Equal(0.7))

		Expect(off.For(&BackendNode{}).ReclaimerEnabled).To(BeFalse(), "a node with no threshold of its own follows the cluster")
	})

	It("refuses values out of range", func() {
		Expect(NodeLimits{MaxActiveBackends: ptr(-1)}.Validate()).ToNot(Succeed())
		Expect(NodeLimits{MemoryReclaimerThreshold: ptr(1.5)}.Validate()).ToNot(Succeed())
		Expect(NodeLimits{VRAMBudget: ptr("lots")}.Validate()).ToNot(Succeed())
		Expect(NodeLimits{VRAMBudget: ptr("")}.Validate()).To(Succeed())
	})
})

var _ = Describe("capAvailable", func() {
	const gb = uint64(1000 * 1000 * 1000)

	It("subtracts the reserve the budget keeps out of reach", func() {
		Expect(capAvailable(20*gb, 12*gb, 24*gb)).To(Equal(8 * gb))
		Expect(capAvailable(10*gb, 12*gb, 24*gb)).To(BeZero())
		Expect(capAvailable(24*gb, 12*gb, 24*gb)).To(Equal(12 * gb))
	})

	It("leaves the reading alone with no budget and caps it without a total", func() {
		Expect(capAvailable(20*gb, 0, 24*gb)).To(Equal(20 * gb))
		Expect(capAvailable(20*gb, 12*gb, 0)).To(Equal(12 * gb))
	})

	It("clamps the scheduling capacity of a node with a 40GB budget on a wrong 188 GiB total (#13531)", func() {
		const detected = uint64(202325295104)
		ceiling := (&NodeRegistry{}).resolveVRAMBudgetBytes("40GB", detected)
		Expect(ceiling).To(Equal(40 * gb))
		Expect(capAvailable(detected, ceiling, detected)).To(Equal(40 * gb))
	})
})

var _ = Describe("Node limits in the registry", func() {
	const gb = uint64(1000 * 1000 * 1000)

	var (
		db       *gorm.DB
		registry *NodeRegistry
		policy   MemoryPolicy
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		policy = DefaultMemoryPolicy()
		registry.SetMemoryPolicy(func() MemoryPolicy { return policy })
	})

	register := func(ctx context.Context, name string, worker NodeLimits) *BackendNode {
		n := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: "10.0.0.1:50051", TotalVRAM: 16 * gb, AvailableVRAM: 16 * gb}
		if worker.VRAMBudget != nil {
			n.VRAMBudget = *worker.VRAMBudget
		}
		n.SetWorkerLimits(worker)
		Expect(registry.Register(ctx, n, true)).To(Succeed())
		got, err := registry.Get(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		return got
	}

	It("applies the cluster VRAM budget to a worker that sets none", func(ctx SpecContext) {
		policy.VRAMBudget = "50%"
		n := register(ctx, "limits-1", NodeLimits{})
		Expect(n.VRAMBudget).To(Equal("50%"))
		Expect(n.VRAMBudgetBytes).To(Equal(8 * gb))
		Expect(n.AvailableVRAM).To(Equal(8 * gb))
	})

	It("lets the worker's budget win over the cluster's", func(ctx SpecContext) {
		policy.VRAMBudget = "50%"
		n := register(ctx, "limits-2", NodeLimits{VRAMBudget: ptr("25%")})
		Expect(n.VRAMBudgetBytes).To(Equal(4 * gb))
	})

	It("follows a change of the cluster budget at the next heartbeat", func(ctx SpecContext) {
		n := register(ctx, "limits-3", NodeLimits{})
		Expect(n.VRAMBudgetBytes).To(BeZero())

		policy.VRAMBudget = "75%"
		avail := 16 * gb
		Expect(registry.Heartbeat(ctx, n.ID, &HeartbeatUpdate{AvailableVRAM: &avail})).To(Succeed())

		got, err := registry.Get(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.VRAMBudget).To(Equal("75%"))
		Expect(got.AvailableVRAM).To(Equal(12 * gb))
	})

	It("keeps admin overrides across a re-registration and clears them on reset", func(ctx SpecContext) {
		n := register(ctx, "limits-4", NodeLimits{MaxActiveBackends: ptr(3)})
		Expect(registry.SetNodeLimits(ctx, n.ID, NodeLimits{MaxActiveBackends: ptr(1), ModelSwap: ptr(false), VRAMBudget: ptr("50%")})).To(Succeed())

		n = register(ctx, "limits-4", NodeLimits{MaxActiveBackends: ptr(5)})
		p := registry.NodePolicy(n)
		Expect(p.MaxActiveBackends).To(Equal(1))
		Expect(p.ModelSwap).To(BeFalse())
		Expect(p.VRAMBudget).To(Equal("50%"))
		Expect(p.Sources["vram_budget"]).To(Equal(LimitSourceOverride))

		Expect(registry.ResetNodeLimits(ctx, n.ID)).To(Succeed())
		n, err := registry.Get(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		p = registry.NodePolicy(n)
		Expect(p.MaxActiveBackends).To(Equal(5))
		Expect(p.Sources["max_active_backends"]).To(Equal(LimitSourceWorker))
		Expect(p.ModelSwap).To(BeTrue())
	})

	It("keeps the budget of a row written before node limits existed", func(ctx SpecContext) {
		n := register(ctx, "limits-5", NodeLimits{})
		Expect(db.Model(&BackendNode{}).Where("id = ?", n.ID).Updates(map[string]any{
			ColLimitsWorker: "", ColVRAMBudget: "50%", ColVRAMBudgetBytes: 8 * gb,
		}).Error).To(Succeed())

		avail := 16 * gb
		Expect(registry.Heartbeat(ctx, n.ID, &HeartbeatUpdate{AvailableVRAM: &avail})).To(Succeed())

		got, err := registry.Get(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.VRAMBudget).To(Equal("50%"))
		Expect(got.AvailableVRAM).To(Equal(8 * gb))
	})
})
