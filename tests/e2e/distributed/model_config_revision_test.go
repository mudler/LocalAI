package distributed_test

import (
	"context"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type revisionCleanupStopper struct {
	mu          sync.Mutex
	unreachable string
	stopped     []nodes.NodeModel
}

func (s *revisionCleanupStopper) StopModelReplica(_ context.Context, nodeID string, replica nodes.NodeModel, _ bool) (messaging.ModelStopReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, replica)
	if nodeID == s.unreachable {
		return messaging.ModelStopReply{}, errors.New("worker unreachable")
	}
	return messaging.ModelStopReply{
		Matched:    true,
		Terminated: true,
		ProcessKey: replica.ModelName,
		Address:    replica.Address,
	}, nil
}

var _ = Describe("distributed model configuration revisions", Label("Distributed"), func() {
	It("quarantines old replicas cluster-wide and converges after an unreachable worker re-registers", func() {
		infra := SetupInfra("localai_model_revision_test")
		ctx := infra.Ctx

		db, err := gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).NotTo(HaveOccurred())
		frontendA, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		// A separate registry instance represents another frontend sharing the
		// PostgreSQL coordination boundary.
		frontendB, err := nodes.NewNodeRegistry(db.Session(&gorm.Session{NewDB: true}))
		Expect(err).NotTo(HaveOccurred())

		workerA := &nodes.BackendNode{Name: "revision-worker-a", Address: "10.0.0.1:9001"}
		workerB := &nodes.BackendNode{Name: "revision-worker-b", Address: "10.0.0.2:9002"}
		Expect(frontendA.Register(ctx, workerA, true)).To(Succeed())
		Expect(frontendA.Register(ctx, workerB, true)).To(Succeed())

		const (
			model       = "ornith"
			oldRevision = "revision-8k"
			newRevision = "revision-100k-parallel-4"
		)
		oldOptions := &pb.ModelOptions{ContextSize: 8192, Options: []string{"parallel:1"}}
		newOptionsA := &pb.ModelOptions{ContextSize: 100000, Options: []string{"parallel:4", "gpu-layers:80"}}
		newOptionsB := &pb.ModelOptions{ContextSize: 100000, Options: []string{"parallel:4", "gpu-layers:60"}}
		oldHash, err := config.EffectiveModelOptionsHash(oldOptions)
		Expect(err).NotTo(HaveOccurred())
		newHashA, err := config.EffectiveModelOptionsHash(newOptionsA)
		Expect(err).NotTo(HaveOccurred())
		newHashB, err := config.EffectiveModelOptionsHash(newOptionsB)
		Expect(err).NotTo(HaveOccurred())

		Expect(frontendA.EstablishModelConfigRevision(ctx, model, oldRevision)).To(Succeed())
		Expect(frontendA.SetNodeModelRevision(ctx, workerA.ID, model, 0, "loaded", workerA.Address, 0, oldRevision, oldHash)).To(Succeed())
		Expect(frontendA.SetNodeModelRevision(ctx, workerB.ID, model, 0, "loaded", workerB.Address, 0, oldRevision, oldHash)).To(Succeed())
		Expect(frontendA.UpsertModelLoadInfoRevision(ctx, model, "llama-cpp", oldRevision, []byte("8k-parallel-1"))).To(Succeed())

		// Before the edit, either frontend can claim an old-generation replica.
		_, claimed, err := frontendB.FindAndLockNodeWithModel(ctx, model, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed.ConfigRevision).To(Equal(oldRevision))
		Expect(frontendB.DecrementInFlight(ctx, claimed.NodeID, model, claimed.ReplicaIndex)).To(Succeed())

		quarantined, err := frontendA.AdvanceModelConfigRevision(ctx, model, newRevision)
		Expect(err).NotTo(HaveOccurred())
		Expect(quarantined).To(HaveLen(2))

		// The other frontend immediately observes the transaction: no stale or
		// unloading replica can receive a request, and a late durable completion
		// cannot restore old replay options.
		_, claimed, err = frontendB.FindAndLockNodeWithModel(ctx, model, nil, nil)
		Expect(err).To(MatchError(gorm.ErrRecordNotFound))
		Expect(claimed).To(BeNil())
		Expect(frontendB.UpsertModelLoadInfoRevision(ctx, model, "llama-cpp", oldRevision, []byte("late-old-load"))).To(MatchError(nodes.ErrStaleModelConfigRevision))

		// A backend load that began before the edit may finish after the new
		// revision is current. Publishing that late completion must fail at the
		// shared registry boundary and must not revive the quarantined replica.
		Expect(frontendB.SetNodeModelRevision(ctx, workerA.ID, model, 0, "loaded", workerA.Address, 0, oldRevision, oldHash)).To(MatchError(nodes.ErrStaleModelConfigRevision))
		modelsAfterLatePublication, err := frontendA.GetNodeModels(ctx, workerA.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(modelsAfterLatePublication).NotTo(ContainElement(And(
			HaveField("ConfigRevision", oldRevision),
			HaveField("State", "loaded"),
		)))

		stopper := &revisionCleanupStopper{unreachable: workerB.ID}
		pending := nodes.NewModelCleanupService(frontendA, stopper).Cleanup(ctx, quarantined, false)
		Expect(pending).To(Equal(1))
		modelsA, err := frontendB.GetNodeModels(ctx, workerA.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(modelsA).To(BeEmpty(), "confirmed exact stop removes only its claimed row")
		modelsB, err := frontendB.GetNodeModels(ctx, workerB.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(modelsB).To(HaveLen(1))
		Expect(modelsB[0].State).To(Equal("unloading"))
		Expect(modelsB[0].CleanupError).To(ContainSubstring("unreachable"))
		Expect(modelsB[0].CleanupNextRetryAt).NotTo(BeNil())

		stopper.mu.Lock()
		stopped := append([]nodes.NodeModel(nil), stopper.stopped...)
		stopper.mu.Unlock()
		Expect(stopped).To(HaveLen(2))
		Expect(stopped).To(ConsistOf(
			HaveField("ConfigRevision", oldRevision),
			HaveField("ConfigRevision", oldRevision),
		))

		// Re-registration is the recovery path for the unreachable worker. It
		// clears its quarantined process row without rolling back current state.
		restartedB := &nodes.BackendNode{Name: workerB.Name, Address: "10.0.0.2:9012"}
		Expect(frontendB.Register(ctx, restartedB, true)).To(Succeed())
		modelsB, err = frontendA.GetNodeModels(ctx, restartedB.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(modelsB).To(BeEmpty())
		current, err := frontendB.GetModelConfigRevision(ctx, model)
		Expect(err).NotTo(HaveOccurred())
		Expect(current).To(Equal(newRevision))

		// The model can now converge on the new context and parallel setting.
		// Hardware-specific effective options may differ without changing the
		// semantic configuration revision.
		Expect(frontendA.SetNodeModelRevision(ctx, workerA.ID, model, 0, "loaded", workerA.Address, 0, newRevision, newHashA)).To(Succeed())
		Expect(frontendB.SetNodeModelRevision(ctx, restartedB.ID, model, 0, "loaded", restartedB.Address, 0, newRevision, newHashB)).To(Succeed())
		Expect(frontendA.UpsertModelLoadInfoRevision(ctx, model, "llama-cpp", newRevision, []byte("100k-parallel-4"))).To(Succeed())

		for range 8 {
			_, replica, claimErr := frontendB.FindAndLockNodeWithModel(ctx, model, nil, nil)
			Expect(claimErr).NotTo(HaveOccurred())
			Expect(replica.ConfigRevision).To(Equal(newRevision))
			Expect(replica.EffectiveOptionsHash).To(Or(Equal(newHashA), Equal(newHashB)))
			Expect(frontendB.DecrementInFlight(ctx, replica.NodeID, model, replica.ReplicaIndex)).To(Succeed())
		}

		backend, replayRevision, replay, err := frontendB.GetModelLoadInfoRevision(ctx, model)
		Expect(err).NotTo(HaveOccurred())
		Expect(backend).To(Equal("llama-cpp"))
		Expect(replayRevision).To(Equal(newRevision))
		Expect(string(replay)).To(Equal("100k-parallel-4"))
	})
})
