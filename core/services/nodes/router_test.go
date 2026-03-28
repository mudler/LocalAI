package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"gorm.io/gorm"
)

// fakeFileStager is a minimal FileStager that records calls and returns
// predictable remote paths without touching the filesystem or network.
type fakeFileStager struct {
	ensureCalls []ensureCall
}

type ensureCall struct {
	nodeID, localPath, key string
}

func (f *fakeFileStager) EnsureRemote(_ context.Context, nodeID, localPath, key string) (string, error) {
	f.ensureCalls = append(f.ensureCalls, ensureCall{nodeID, localPath, key})
	return "/remote/" + key, nil
}

func (f *fakeFileStager) FetchRemote(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeFileStager) FetchRemoteByKey(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeFileStager) AllocRemoteTemp(_ context.Context, _ string) (string, error) {
	return "/remote/tmp", nil
}

func (f *fakeFileStager) StageRemoteToStore(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeFileStager) ListRemoteDir(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

var _ = Describe("SmartRouter", func() {
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

	Describe("evictLRUAndFreeNode", func() {
		It("returns ErrEvictionBusy in under 5 seconds when all models are busy", func() {
			node := &BackendNode{
				Name:     "busy-evict",
				NodeType: NodeTypeBackend,
				Address:  "10.0.0.100:50051",
			}
			Expect(registry.Register(node, true)).To(Succeed())

			// Load a model and give it in-flight requests so it cannot be evicted
			Expect(registry.SetNodeModel(node.ID, "busy-model", "loaded")).To(Succeed())
			Expect(registry.IncrementInFlight(node.ID, "busy-model")).To(Succeed())

			router := NewSmartRouter(registry, SmartRouterOptions{DB: db})

			start := time.Now()
			_, err := router.evictLRUAndFreeNode(context.Background())
			elapsed := time.Since(start)

			Expect(err).To(MatchError(ErrEvictionBusy))
			// 5 retries * 500ms = 2.5s nominal; allow generous upper bound
			Expect(elapsed).To(BeNumerically("<", 5*time.Second))
		})

		It("respects context cancellation", func() {
			node := &BackendNode{
				Name:     "cancel-evict",
				NodeType: NodeTypeBackend,
				Address:  "10.0.0.101:50051",
			}
			Expect(registry.Register(node, true)).To(Succeed())
			Expect(registry.SetNodeModel(node.ID, "cancel-model", "loaded")).To(Succeed())
			Expect(registry.IncrementInFlight(node.ID, "cancel-model")).To(Succeed())

			router := NewSmartRouter(registry, SmartRouterOptions{DB: db})

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancel immediately

			start := time.Now()
			_, err := router.evictLRUAndFreeNode(ctx)
			elapsed := time.Since(start)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("context cancelled"))
			// Should return very quickly since context is already done
			Expect(elapsed).To(BeNumerically("<", 2*time.Second))
		})
	})

	Describe("stageModelFiles", func() {
		It("does not mutate the original ModelOptions", func() {
			stager := &fakeFileStager{}
			router := NewSmartRouter(registry, SmartRouterOptions{
				FileStager: stager,
				DB:         db,
			})

			node := &BackendNode{
				ID:      "stage-node-id",
				Name:    "stage-node",
				Address: "10.0.0.200:50051",
			}

			original := &pb.ModelOptions{
				Model:     "test-backend/models/test.gguf",
				ModelFile: "/models/test-backend/models/test.gguf",
				MMProj:    "",
			}

			// Capture original values before staging
			origModel := original.Model
			origModelFile := original.ModelFile
			origMMProj := original.MMProj

			// stageModelFiles creates temp files for os.Stat checks.
			// Since none of our test paths exist on disk, stageModelFiles will
			// skip them (clearing non-existent optional fields). The key property
			// is that the original proto pointer is not modified.
			_, _ = router.stageModelFiles(context.Background(), node, original)

			// Verify the original proto was not mutated
			Expect(original.Model).To(Equal(origModel))
			Expect(original.ModelFile).To(Equal(origModelFile))
			Expect(original.MMProj).To(Equal(origMMProj))
		})
	})
})
