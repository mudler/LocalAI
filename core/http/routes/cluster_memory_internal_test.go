package routes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/system"
)

// Every model-sizing surface on a distributed controller reads the cluster
// through these seams, and each one degrades to the controller's own hardware
// rather than to "nothing fits".
var _ = Describe("cluster memory resolution", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	reading := &nodes.ClusterMemory{
		NodeID: "n-1", NodeName: "dgx-01", TotalMemory: 80 * 1024 * 1024 * 1024,
		IsGPU: true, NodeCount: 4,
	}

	Describe("resolveClusterMemory", func() {
		It("reports nothing in single-node mode", func() {
			Expect(resolveClusterMemory(context.Background(), nil)).To(BeNil())
		})

		It("reports the provider's reading", func() {
			provider := func(context.Context) (*nodes.ClusterMemory, error) { return reading, nil }

			Expect(resolveClusterMemory(context.Background(), provider)).To(Equal(reading))
		})

		// A registry hiccup must never mark the whole catalog as too large.
		It("degrades to no reading when the registry errors", func() {
			provider := func(context.Context) (*nodes.ClusterMemory, error) {
				return nil, errors.New("connection refused")
			}

			Expect(resolveClusterMemory(context.Background(), provider)).To(BeNil())
		})
	})

	Describe("clusterResourceBlock", func() {
		It("reports nothing to serialize when there is no reading", func() {
			Expect(clusterResourceBlock(nil)).To(BeNil())
		})

		// The node name travels with the number because "fits" is only ever
		// meaningful somewhere, and the UI says where.
		It("names the node the budget belongs to", func() {
			block := clusterResourceBlock(reading)

			Expect(block).To(HaveKeyWithValue("enabled", true))
			Expect(block).To(HaveKeyWithValue("node_count", 4))
			Expect(block).To(HaveKeyWithValue("total_memory", gib(80)))
			Expect(block).To(HaveKeyWithValue("node_name", "dgx-01"))
			Expect(block).To(HaveKeyWithValue("node_id", "n-1"))
			Expect(block).To(HaveKeyWithValue("is_gpu", true))
		})
	})

	Describe("clusterModelEnv", func() {
		controller := system.NewCapabilityState("default")

		It("describes the controller when no provider is wired", func() {
			env := clusterModelEnv(context.Background(), controller, nil, nil)

			Expect(env.AvailableMemory).To(Equal(hostModelEnv(context.Background(), controller).AvailableMemory))
			Expect(env.BackendCompatible("cuda-13-vllm")).To(BeFalse())
		})

		It("describes the cluster when both providers answer", func() {
			memProvider := func(context.Context) (*nodes.ClusterMemory, error) { return reading, nil }
			capProvider := func(context.Context) ([]string, error) {
				return []string{"nvidia-cuda-13"}, nil
			}

			env := clusterModelEnv(context.Background(), controller, memProvider, capProvider)

			Expect(env.AvailableMemory).To(Equal(gib(80)))
			Expect(env.BackendCompatible("cuda-13-vllm")).To(BeTrue())
		})

		// Half an answer is still better than the controller's: a cluster that
		// reports hardware but no usable memory keeps the hardware verdict.
		It("uses what the cluster could answer when the memory reading is missing", func() {
			capProvider := func(context.Context) ([]string, error) {
				return []string{"nvidia-cuda-13"}, nil
			}

			env := clusterModelEnv(context.Background(), controller, nil, capProvider)

			Expect(env.BackendCompatible("cuda-13-vllm")).To(BeTrue())
			Expect(env.AvailableMemory).To(Equal(hostModelEnv(context.Background(), controller).AvailableMemory))
		})
	})
})
