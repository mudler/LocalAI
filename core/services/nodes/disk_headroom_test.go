package nodes

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"gorm.io/gorm"
)

// These specs encode the production incident behind this fix: a Jetson Thor
// worker whose models filesystem was 937G/937G/0-avail kept reporting
// `status: healthy`, was picked to host a 70GB model, accepted the staging
// request, transferred ~17GB and only then failed with
// "no space left on device" — 16 minutes after the decision that could never
// have worked.
var _ = Describe("Node disk headroom", func() {
	const gb = uint64(1000 * 1000 * 1000)

	Describe("DiskRequirementFor", func() {
		It("demands the model's payload plus a safety margin", func() {
			// A 70GB checkpoint must require MORE than 70GB free: the staging
			// write is not the only thing landing on that filesystem.
			req := DiskRequirementFor(int64(70 * gb))
			Expect(req).To(BeNumerically(">", 70*gb))
			// ...but the margin must stay modest, or a tight-but-usable node
			// gets stranded out of rotation.
			Expect(req).To(BeNumerically("<", 80*gb))
		})

		It("falls back to a small absolute floor when the model size is unknown", func() {
			// Bare-HF-repo models have no local payload to stat. We must not
			// demand 0 (which lets a completely full node back in) nor
			// something large (which would strand a healthy small node).
			req := DiskRequirementFor(0)
			Expect(req).To(BeNumerically(">", 0))
			Expect(req).To(BeNumerically("<=", 4*gb))
		})
	})

	Describe("nodesWithDiskHeadroom", func() {
		It("drops a node whose models filesystem is full", func() {
			full := BackendNode{
				Name: "nvidia-thor", TotalDisk: 937 * gb, AvailableDisk: 0,
			}
			roomy := BackendNode{
				Name: "worker-roomy", TotalDisk: 2000 * gb, AvailableDisk: 1200 * gb,
			}

			fit := nodesWithDiskHeadroom([]BackendNode{full, roomy}, DiskRequirementFor(int64(70*gb)))

			Expect(nodeNames(fit)).To(ConsistOf("worker-roomy"))
		})

		It("drops a node that is merely too small for THIS model but keeps it for a smaller one", func() {
			tight := BackendNode{Name: "worker-tight", TotalDisk: 100 * gb, AvailableDisk: 20 * gb}

			big := nodesWithDiskHeadroom([]BackendNode{tight}, DiskRequirementFor(int64(70*gb)))
			Expect(big).To(BeEmpty())

			small := nodesWithDiskHeadroom([]BackendNode{tight}, DiskRequirementFor(int64(2*gb)))
			Expect(nodeNames(small)).To(ConsistOf("worker-tight"))
		})

		It("keeps nodes that do not report disk at all", func() {
			// A pre-upgrade worker reports total_disk == 0. Excluding it would
			// take the whole cluster out of rotation on a rolling upgrade.
			legacy := BackendNode{Name: "worker-legacy", TotalDisk: 0, AvailableDisk: 0}

			fit := nodesWithDiskHeadroom([]BackendNode{legacy}, DiskRequirementFor(int64(70*gb)))

			Expect(nodeNames(fit)).To(ConsistOf("worker-legacy"))
		})
	})

	Describe("NarrowByDiskHeadroom", func() {
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

		seed := func(ctx context.Context, name string, totalDisk, availDisk uint64) string {
			node := &BackendNode{
				Name:          name,
				NodeType:      NodeTypeBackend,
				Address:       "10.0.0.1:50051",
				TotalDisk:     totalDisk,
				AvailableDisk: availDisk,
			}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			return node.ID
		}

		It("removes a full node from the candidate set", func(ctx SpecContext) {
			fullID := seed(ctx, "nvidia-thor", 937*gb, 0)
			roomyID := seed(ctx, "worker-roomy", 2000*gb, 1200*gb)

			got, err := registry.NarrowByDiskHeadroom(ctx, []string{fullID, roomyID}, DiskRequirementFor(int64(70*gb)))

			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(ConsistOf(roomyID))
		})

		It("fails with a capacity error naming the shortfall when no node can store the model", func(ctx SpecContext) {
			fullID := seed(ctx, "nvidia-thor", 937*gb, 0)

			_, err := registry.NarrowByDiskHeadroom(ctx, []string{fullID}, DiskRequirementFor(int64(70*gb)))

			Expect(err).To(MatchError(ErrInsufficientDisk))
			// The operator must learn this at scheduling time, with the numbers
			// that made the decision — not 16 minutes into a transfer.
			Expect(err.Error()).To(ContainSubstring("nvidia-thor"))
		})
	})
})

var _ = Describe("scheduling a model onto a cluster without disk headroom", func() {
	var (
		reg      *fakeModelRouter
		backend  *holdBackend
		unloader *fakeUnloader
		router   *SmartRouter
		dir      string
	)

	BeforeEach(func() {
		reg = &fakeModelRouter{findAndLockErr: errors.New("not found")}
		reg.findIdleNode = &BackendNode{ID: "n1", Name: "nvidia-thor", Address: "10.0.0.1:50051"}
		backend = &holdBackend{}
		unloader = &fakeUnloader{
			installReply: &messaging.BackendInstallReply{Success: true, Address: "10.0.0.1:9001"},
		}
		router = NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: &holdClientFactory{client: backend},
		})
		dir = GinkgoT().TempDir()
	})

	route := func(modelFile string) error {
		_, err := router.Route(context.Background(), "longcat-video-avatar-1.5", "models/big.gguf", "llama-cpp",
			&pb.ModelOptions{Model: "models/big.gguf", ModelFile: modelFile}, false)
		return err
	}

	It("fails at scheduling time instead of installing a backend it cannot feed", func() {
		reg.narrowByDiskErr = fmt.Errorf("%w: nvidia-thor has 0 B free of 937.0 GB", ErrInsufficientDisk)

		err := route(sparseCheckpoint(dir, "big.gguf", 70<<30))

		Expect(err).To(MatchError(ErrInsufficientDisk))
		// The whole point is that nothing expensive happened: no backend
		// install, so no staging, so no 17GB transferred over 16 minutes
		// before the truth surfaced.
		Expect(unloader.installCalls).To(BeEmpty())
	})

	It("restores the pre-check behaviour when the operator disables the knob", func() {
		// LOCALAI_DISTRIBUTED_DISK_HEADROOM_CHECK=false (or the runtime
		// toggle) must give back exactly today's behaviour: the node that
		// lacks space IS selected and the load proceeds.
		reg.narrowByDiskErr = fmt.Errorf("%w: nvidia-thor has 0 B free of 937.0 GB", ErrInsufficientDisk)
		router = NewSmartRouter(reg, SmartRouterOptions{
			Unloader:            unloader,
			ClientFactory:       &holdClientFactory{client: backend},
			DiskHeadroomEnabled: func() bool { return false },
		})

		Expect(route(sparseCheckpoint(dir, "big.gguf", 70<<30))).To(Succeed())
		Expect(unloader.installCalls).To(HaveLen(1))
	})

	It("still evaluates the check when disabled, so the operator is not left blind", func() {
		// Disabled means "do not block", NOT "do not look". A silently
		// disabled safety check is how the original bug stayed invisible.
		reg.narrowByDiskErr = fmt.Errorf("%w: nvidia-thor has 0 B free of 937.0 GB", ErrInsufficientDisk)
		router = NewSmartRouter(reg, SmartRouterOptions{
			Unloader:            unloader,
			ClientFactory:       &holdClientFactory{client: backend},
			DiskHeadroomEnabled: func() bool { return false },
		})

		Expect(route(sparseCheckpoint(dir, "big.gguf", 70<<30))).To(Succeed())
		Expect(reg.narrowByDiskRequired).ToNot(BeEmpty(),
			"the disabled check must still run so it can warn about the shortfall")
	})

	It("reads the toggle live, so a runtime change applies without a restart", func() {
		// The router is constructed ONCE; the operator flips the setting
		// afterwards. Snapshotting the value at construction would make the
		// runtime setting a lie.
		enabled := true
		reg.narrowByDiskErr = fmt.Errorf("%w: nvidia-thor has 0 B free of 937.0 GB", ErrInsufficientDisk)
		router = NewSmartRouter(reg, SmartRouterOptions{
			Unloader:            unloader,
			ClientFactory:       &holdClientFactory{client: backend},
			DiskHeadroomEnabled: func() bool { return enabled },
		})

		Expect(route(sparseCheckpoint(dir, "big.gguf", 70<<30))).To(MatchError(ErrInsufficientDisk))

		enabled = false
		Expect(route(sparseCheckpoint(dir, "big2.gguf", 70<<30))).To(Succeed())
	})

	It("skips the check in shared-models mode, where nothing is staged to the worker", func() {
		// With LOCALAI_DISTRIBUTED_SHARED_MODELS every node mounts the same
		// models directory and stageModelFiles uploads nothing, so demanding
		// 73GB free per node would reject a cluster that needs no new bytes
		// at all.
		reg.narrowByDiskErr = fmt.Errorf("%w: nvidia-thor has 0 B free of 937.0 GB", ErrInsufficientDisk)
		router = NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: &holdClientFactory{client: backend},
			SharedModels:  true,
		})

		Expect(route(sparseCheckpoint(dir, "big.gguf", 70<<30))).To(Succeed())
		Expect(reg.narrowByDiskRequired).To(BeEmpty(),
			"shared-models mode stages nothing, so the check must not run at all")
	})

	It("sizes the disk requirement from the model, not from a fixed threshold", func() {
		Expect(route(sparseCheckpoint(dir, "big.gguf", 70<<30))).To(Succeed())

		Expect(reg.narrowByDiskRequired).ToNot(BeEmpty())
		// A 70 GiB checkpoint must ask for more than 70 GiB of free space.
		Expect(reg.narrowByDiskRequired[0]).To(BeNumerically(">", uint64(70)<<30))
	})
})

func nodeNames(list []BackendNode) []string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		out = append(out, n.Name)
	}
	return out
}
