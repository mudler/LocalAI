package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// A waiter finds its job by generation. A live row with no generation (one a
// frontend has not backfilled yet) must still be waited on, found by its model
// and node. Read by generation alone it looked gone at once, so the request
// ran out of rounds with "the load finished but the model is not available"
// while the load was running.
var _ = Describe("Waiting on a load job row with no generation", func() {
	It("waits for the load instead of taking it for finished", func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx := context.Background()
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		node := &BackendNode{Name: "only", NodeType: NodeTypeBackend, Address: "only:50051", MaxReplicasPerModel: 1}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		node, err = registry.GetByName(ctx, "only")
		Expect(err).ToNot(HaveOccurred())
		now := time.Now()
		Expect(db.Exec(`INSERT INTO model_load_jobs (tracking_key, node_id, node_name, generation, state, owner_replica, last_progress, progress_at, created_at, updated_at, lease_until)
			VALUES (?, ?, ?, '', 'loading', 'old-frontend', ?, ?, ?, ?, now() + interval '1 minute')`,
			"model-x", node.ID, node.Name, now, now, now, now).Error).To(Succeed())

		unloader := &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001"}}
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		router := NewSmartRouter(registry, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend},
			DB: db, ModelLoadWait: 3 * time.Second})

		_, err = router.Route(ctx, "model-x", "models/x.gguf", "llama-cpp", "", &pb.ModelOptions{Model: "models/x.gguf"}, false)

		var loading *ModelLoadingError
		Expect(err).To(BeAssignableToTypeOf(loading), "got %v", err)
		Expect(err.(*ModelLoadingError).Status.State).To(Equal(LoadJobStateLoading))
		Expect(err.Error()).NotTo(ContainSubstring("the load finished"))
		Expect(unloader.installCalls).To(BeEmpty(), "no second load on the node")
	})
})
