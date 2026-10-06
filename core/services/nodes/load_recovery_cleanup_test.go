package nodes

import (
	"context"
	"errors"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"time"
)

var _ = Describe("Recovery cleanup quarantine", func() {
	for _, action := range []string{"reregister", "offline", "health"} {
		action := action
		It("preserves uncertain slot across "+action, func() {
			ctx := context.Background()
			registry, err := NewNodeRegistry(testutil.SetupTestDB())
			Expect(err).NotTo(HaveOccurred())
			node := &BackendNode{Name: "review-worker", NodeType: NodeTypeBackend, Address: "127.0.0.1:1234", WorkerIncarnation: "boot-one"}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			job, claimed, err := registry.ClaimLoadJob(ctx, "review-model", "review-owner")
			Expect(err).NotTo(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(registry.SetNodeModel(ctx, node.ID, job.TrackingKey, 0, "loading", "127.0.0.1:1235", 0)).To(Succeed())
			_, err = (&LoadRecoveryService{Registry: registry}).Fail(ctx, job.Ref(), "work uncertain")
			Expect(err).NotTo(HaveOccurred())
			_, err = registry.NextFreeReplicaIndex(ctx, node.ID, job.TrackingKey, 1)
			Expect(err).To(MatchError(ErrNoFreeSlot))
			if action == "reregister" {
				replacement := &BackendNode{Name: node.Name, NodeType: NodeTypeBackend, Address: node.Address, WorkerIncarnation: "boot-two"}
				Expect(registry.Register(ctx, replacement, true)).To(Succeed())
				Expect(replacement.ID).To(Equal(node.ID))
			} else if action == "offline" {
				Expect(registry.MarkOffline(ctx, node.ID)).To(Succeed())
			} else {
				hm := NewHealthMonitor(registry, nil, time.Second, time.Hour, "", true, &stubClientFactory{client: &stubBackend{healthResult: false}})
				for i := 0; i < perModelMissThreshold; i++ {
					hm.doCheckAll(ctx)
				}
			}
			actual, err := registry.GetLoadJob(ctx, job.TrackingKey)
			Expect(err).NotTo(HaveOccurred())
			Expect(actual.WorkUncertain).To(BeTrue())
			_, err = registry.NextFreeReplicaIndex(ctx, node.ID, job.TrackingKey, 1)
			Expect(err).To(MatchError(ErrNoFreeSlot), "uncertain reservation must survive without whole-operation termination evidence")
		})
	}
})

var _ = Describe("Recovery cleanup quarantine reconciler", func() {
	It("retains the reservation and durable uncertainty after a reconciler load fails", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		node := &BackendNode{Name: "replication-worker", NodeType: NodeTypeBackend, Address: "127.0.0.1:1234", TotalVRAM: 64_000_000_000, AvailableVRAM: 64_000_000_000}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		blob, err := proto.Marshal(&pb.ModelOptions{Model: "synthetic.gguf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.SetNodeModel(ctx, node.ID, "replication-model", 0, "loaded", node.Address, 0)).To(Succeed())
		Expect(registry.SetNodeModelLoadInfo(ctx, node.ID, "replication-model", 0, "backend", blob)).To(Succeed())
		Expect(registry.UpsertModelLoadInfo(ctx, "replication-model", "backend", blob)).To(Succeed())
		Expect(registry.RemoveNodeModel(ctx, node.ID, "replication-model", 0)).To(Succeed())
		router := NewSmartRouter(registry, SmartRouterOptions{DB: db, Unloader: &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: node.Address}}, ClientFactory: &stubClientFactory{client: &stubBackend{healthResult: true, loadErr: errors.New("remote outcome unknown")}}})
		_, err = router.ScheduleAndLoadModel(ctx, "replication-model", nil)
		Expect(err).To(MatchError(ContainSubstring("remote outcome unknown")))
		_, err = registry.NextFreeReplicaIndex(ctx, node.ID, "replication-model", 1)
		Expect(err).To(MatchError(ErrNoFreeSlot))
		job, err := registry.GetLoadJob(ctx, "replication-model")
		Expect(err).NotTo(HaveOccurred())
		Expect(job).NotTo(BeNil())
		Expect(job.WorkUncertain).To(BeTrue())
		Expect(job.State).To(Equal(LoadJobStateFailed))
	})
})

var _ = Describe("Recovery cleanup quarantine mutations", func() {
	It("rejects stale generation cleanup and publication without blocking a healthy unload", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		a, _, err := registry.ClaimLoadJob(ctx, "model", "a")
		Expect(err).NotTo(HaveOccurred())
		aCtx := context.WithValue(ctx, loadOwnershipKey{}, a.Ref())
		Expect(registry.SetNodeModel(aCtx, "node", "model", 0, "loaded", "old", 0)).To(Succeed())
		Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())
		b, _, err := registry.ClaimLoadJob(ctx, "model", "b")
		Expect(err).NotTo(HaveOccurred())
		bCtx := context.WithValue(ctx, loadOwnershipKey{}, b.Ref())
		Expect(registry.SetNodeModel(bCtx, "node", "model", 1, "loading", "new", 0)).To(Succeed())
		Expect(registry.RemoveNodeModel(aCtx, "node", "model", 1)).To(MatchError(ErrStaleLoadJob))
		Expect(registry.SetNodeModel(aCtx, "node", "model", 1, "loaded", "late", 0)).To(MatchError(ErrStaleLoadJob))
		Expect(registry.SetNodeModel(ctx, "node", "model", 1, "loaded", "anonymous", 0)).To(MatchError(ErrStaleLoadJob))
		Expect(registry.RemoveNodeModel(ctx, "node", "model", 0)).To(Succeed())
		_, err = registry.GetNodeModel(ctx, "node", "model", 0)
		Expect(err).To(HaveOccurred())
		Expect(registry.SetNodeModel(bCtx, "node", "model", 1, "loaded", "new", 0)).To(Succeed())
		Expect(registry.DeleteLoadJob(ctx, b.Ref())).To(Succeed())
		Expect(registry.RemoveNodeModel(ctx, "node", "model", 1)).To(Succeed())
		idx, err := registry.NextFreeReplicaIndex(ctx, "node", "model", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(Equal(0))
		c, claimed, err := registry.ClaimLoadJob(ctx, "model", "c")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(c.Generation).NotTo(Equal(b.Generation))
	})
	It("retains uncertain work through config changes and every cleanup entry point", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		registry, err := NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.SetNodeModelRevision(ctx, "node", "model", 0, "loading", "addr", 0, "old", "")).To(Succeed())
		rows, err := registry.AdvanceModelConfigRevision(ctx, "model", "new")
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		removed, err := registry.RemoveClaimedModelCleanup(ctx, rows[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeFalse())
		Expect(registry.RemoveAllNodeModelReplicas(ctx, "node", "model")).To(Succeed())
		_, err = registry.NextFreeReplicaIndex(ctx, "node", "model", 1)
		Expect(err).To(MatchError(ErrNoFreeSlot))
		Expect(registry.SetNodeModelRevision(ctx, "node", "model", 0, "staging", "replacement", 0, "new", "")).To(MatchError(ErrStaleLoadJob))
	})
})

var _ = Describe("Recovery cleanup quarantine concurrency", func() {
	It("does not let an old health observation delete a replacement", func() {
		ctx := context.Background()
		registry, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.SetNodeModel(ctx, "node", "model", 0, "loaded", "old", 0)).To(Succeed())
		observed, err := registry.GetNodeModel(ctx, "node", "model", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(registry.SetNodeModel(ctx, "node", "model", 0, "loaded", "replacement", 0)).To(Succeed())
		Expect(registry.RemoveNodeModel(context.WithValue(ctx, replicaObservationKey{}, *observed), "node", "model", 0)).To(Succeed())
		current, err := registry.GetNodeModel(ctx, "node", "model", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Address).To(Equal("replacement"))
	})
	It("serializes competing publication attempts and preserves the uncertain slot", func() {
		ctx := context.Background()
		registry, err := NewNodeRegistry(testutil.SetupTestDB())
		Expect(err).NotTo(HaveOccurred())
		job, _, err := registry.ClaimLoadJob(ctx, "model", "owner")
		Expect(err).NotTo(HaveOccurred())
		owned := context.WithValue(ctx, loadOwnershipKey{}, job.Ref())
		Expect(registry.SetNodeModel(owned, "node", "model", 0, "loading", "old", 0)).To(Succeed())
		gate := make(chan struct{})
		done := make(chan error, 2)
		go func() { <-gate; done <- registry.FailLoadJob(ctx, job.Ref(), "uncertain") }()
		go func() { <-gate; done <- registry.RemoveNodeModel(ctx, "node", "model", 0) }()
		close(gate)
		Expect(<-done).To(Succeed())
		Expect(<-done).To(Succeed())
		Expect(registry.SetNodeModel(owned, "node", "model", 0, "loaded", "late", 0)).To(MatchError(ErrStaleLoadJob))
		_, err = registry.NextFreeReplicaIndex(ctx, "node", "model", 1)
		Expect(err).To(MatchError(ErrNoFreeSlot))
	})
})
