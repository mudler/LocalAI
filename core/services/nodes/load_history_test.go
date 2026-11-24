package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("loadHistory", func() {
	It("returns nothing for a node it has not seen", func() {
		var h loadHistory
		Expect(h.snapshot("n1")).To(BeNil())
	})

	It("keeps samples oldest first", func() {
		var h loadHistory
		for _, v := range []float64{10, 20, 30} {
			h.add("n1", v)
		}
		Expect(h.snapshot("n1")).To(Equal([]float64{10, 20, 30}))
	})

	It("drops the oldest samples once the ring is full", func() {
		var h loadHistory
		for i := 1; i <= loadHistorySamples+5; i++ {
			h.add("n1", float64(i))
		}
		got := h.snapshot("n1")
		Expect(got).To(HaveLen(loadHistorySamples))
		Expect(got[0]).To(Equal(6.0))
		Expect(got[len(got)-1]).To(Equal(float64(loadHistorySamples + 5)))
	})

	It("clamps readings to 0..100", func() {
		var h loadHistory
		h.add("n1", -4)
		h.add("n1", 250)
		Expect(h.snapshot("n1")).To(Equal([]float64{0, 100}))
	})

	It("keeps nodes apart and forgets a removed node", func() {
		var h loadHistory
		h.add("n1", 1)
		h.add("n2", 2)
		h.forget("n1")
		Expect(h.snapshot("n1")).To(BeNil())
		Expect(h.snapshot("n2")).To(Equal([]float64{2}))
	})

	It("returns a copy the caller may change", func() {
		var h loadHistory
		h.add("n1", 5)
		h.snapshot("n1")[0] = 99
		Expect(h.snapshot("n1")).To(Equal([]float64{5}))
	})
})

var _ = Describe("NodeRegistry load history", func() {
	var registry *NodeRegistry
	ctx := context.Background()

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		var err error
		registry, err = NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).ToNot(HaveOccurred())
	})

	f := func(v float64) *float64 { return &v }
	u := func(v uint64) *uint64 { return &v }

	extras := func(id string) NodeWithExtras {
		list, err := registry.ListWithExtras(ctx)
		Expect(err).ToNot(HaveOccurred())
		for _, n := range list {
			if n.ID == id {
				return n
			}
		}
		Fail("node not listed")
		return NodeWithExtras{}
	}

	It("samples VRAM use on a GPU node and CPU use on a CPU node", func() {
		gpu := &BackendNode{Name: "gpu", NodeType: NodeTypeBackend, Address: "gpu:1", TotalVRAM: 100}
		cpu := &BackendNode{Name: "cpu", NodeType: NodeTypeBackend, Address: "cpu:1"}
		Expect(registry.Register(ctx, gpu, true)).To(Succeed())
		Expect(registry.Register(ctx, cpu, true)).To(Succeed())

		Expect(registry.Heartbeat(ctx, gpu.ID, &HeartbeatUpdate{AvailableVRAM: u(75), CPUUsagePercent: f(5)})).To(Succeed())
		Expect(registry.Heartbeat(ctx, gpu.ID, &HeartbeatUpdate{AvailableVRAM: u(50)})).To(Succeed())
		Expect(registry.Heartbeat(ctx, cpu.ID, &HeartbeatUpdate{CPUUsagePercent: f(40)})).To(Succeed())
		Expect(registry.Heartbeat(ctx, cpu.ID, nil)).To(Succeed())

		Expect(extras(gpu.ID).LoadHistory).To(Equal([]float64{25, 50}))
		Expect(extras(cpu.ID).LoadHistory).To(Equal([]float64{40}))
	})

	It("leaves the history empty for a worker that reports no load", func() {
		node := &BackendNode{Name: "old", NodeType: NodeTypeBackend, Address: "old:1"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		Expect(registry.Heartbeat(ctx, node.ID, nil)).To(Succeed())
		Expect(extras(node.ID).LoadHistory).To(BeEmpty())
	})

	It("drops the history when the node is removed", func() {
		node := &BackendNode{Name: "gone", NodeType: NodeTypeBackend, Address: "gone:1"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		Expect(registry.Heartbeat(ctx, node.ID, &HeartbeatUpdate{CPUUsagePercent: f(10)})).To(Succeed())
		Expect(registry.Deregister(ctx, node.ID)).To(Succeed())
		Expect(registry.loadHist.snapshot(node.ID)).To(BeNil())
	})
})
