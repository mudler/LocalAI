package gallery_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// On a distributed controller the GPUs live on the workers, so a variant
// picker sized against the controller tells admins a cluster of A100s can only
// run the smallest CPU build.
var _ = Describe("ClusterResolveEnv", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	// The controller as Argus actually runs it: no GPU at all.
	var controller *system.SystemState

	BeforeEach(func() {
		controller = system.NewCapabilityState("default")
	})

	It("sizes models against the cluster reading rather than the controller", func() {
		env := gallery.ClusterResolveEnv(context.Background(), controller, gib(80), []string{"nvidia-cuda-13"})

		Expect(env.AvailableMemory).To(Equal(gib(80)))
	})

	It("accepts a CUDA backend that only the workers can run", func() {
		env := gallery.ClusterResolveEnv(context.Background(), controller, gib(80), []string{"nvidia-cuda-13"})

		Expect(env.BackendCompatible).ToNot(BeNil())
		// A name carrying the cuda token is what the controller rejects today;
		// a bare engine name like "vllm" passes on any host and would prove
		// nothing about the union.
		Expect(env.BackendCompatible("cuda-13-vllm")).To(BeTrue())
		Expect(env.BackendCompatible("llama-cpp")).To(BeTrue())
	})

	// The union must stay a filter, not an open door: a Linux NVIDIA fleet
	// still cannot run an Apple-only build.
	It("still rejects a backend no node in the cluster can run", func() {
		env := gallery.ClusterResolveEnv(context.Background(), controller, gib(80), []string{"nvidia-cuda-13"})

		Expect(env.BackendCompatible("mlx")).To(BeFalse())
	})

	It("accepts a backend that any one node in a mixed fleet can run", func() {
		env := gallery.ClusterResolveEnv(context.Background(), controller, gib(80), []string{"nvidia-cuda-13", "metal"})

		Expect(env.BackendCompatible("mlx")).To(BeTrue())
		Expect(env.BackendCompatible("cuda-13-vllm")).To(BeTrue())
	})

	// Ranking has to follow the hardware too, or a cluster of NVIDIA workers
	// gets offered the GGUF build over the vLLM one it should prefer.
	It("ranks engines by the workers' hardware, not the controller's", func() {
		env := gallery.ClusterResolveEnv(context.Background(), controller, gib(80), []string{"nvidia-cuda-13"})

		Expect(env.EnginePreference).To(Equal(system.NewCapabilityState("nvidia-cuda-13").EnginePreferenceTokens()))
	})

	// Every degradation path lands here, so it must be indistinguishable from
	// the single-node behavior that shipped before any of this existed.
	It("falls back to the host description when the cluster reports nothing", func() {
		host := gallery.HostResolveEnv(context.Background(), controller)
		env := gallery.ClusterResolveEnv(context.Background(), controller, 0, nil)

		Expect(env.AvailableMemory).To(Equal(host.AvailableMemory))
		Expect(env.EnginePreference).To(Equal(host.EnginePreference))
		Expect(env.BackendCompatible("cuda-13-vllm")).To(Equal(host.BackendCompatible("cuda-13-vllm")))
		Expect(env.BackendCompatible("mlx")).To(Equal(host.BackendCompatible("mlx")))
	})

	// A cluster that reports capabilities but no usable memory reading should
	// still gain the hardware view; only the size question falls back.
	It("keeps the host memory when only the memory reading is missing", func() {
		host := gallery.HostResolveEnv(context.Background(), controller)
		env := gallery.ClusterResolveEnv(context.Background(), controller, 0, []string{"nvidia-cuda-13"})

		Expect(env.AvailableMemory).To(Equal(host.AvailableMemory))
		Expect(env.BackendCompatible("cuda-13-vllm")).To(BeTrue())
	})

	It("keeps the probe wired so variant sizes are still measured", func() {
		env := gallery.ClusterResolveEnv(context.Background(), controller, gib(80), []string{"nvidia-cuda-13"})

		Expect(env.ProbeMemory).ToNot(BeNil())
		Expect(env.ServingFeaturePreference).To(Equal(system.ServingFeaturePreferenceTokens()))
	})
})
