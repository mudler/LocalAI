package distributed_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// slowScheduler places a replica on one node and marks it loaded after a
// delay, like a worker that takes a while to load the model.
type slowScheduler struct {
	registry *nodes.NodeRegistry
	target   *nodes.BackendNode
	delay    time.Duration
	calls    atomic.Int32
}

func (s *slowScheduler) PrepareReplica(_ context.Context, _ string, _ []string) (nodes.PreparedReplica, error) {
	s.calls.Add(1)
	return &slowPrepared{s: s}, nil
}

type slowPrepared struct{ s *slowScheduler }

func (p *slowPrepared) Node() *nodes.BackendNode { return p.s.target }

func (p *slowPrepared) Load(ctx context.Context) (*nodes.BackendNode, error) {
	time.Sleep(p.s.delay)
	return p.s.target, p.s.registry.SetNodeModel(ctx, p.s.target.ID, "drain-model", 0, "loaded", "n2:6000", 0)
}

var _ = Describe("Node drain (replace, then remove)", Label("Distributed"), func() {
	It("moves a min_replicas model from a draining node with no window of zero replicas", func() {
		infra := SetupInfra("localai_drain_test")
		db, err := gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx := context.Background()

		n1 := &nodes.BackendNode{Name: "drain-n1", NodeType: nodes.NodeTypeBackend, Address: "n1:50051", MaxReplicasPerModel: 1}
		n2 := &nodes.BackendNode{Name: "drain-n2", NodeType: nodes.NodeTypeBackend, Address: "n2:50051", MaxReplicasPerModel: 1}
		Expect(registry.Register(ctx, n1, true)).To(Succeed())
		Expect(registry.Register(ctx, n2, true)).To(Succeed())
		Expect(registry.SetModelScheduling(ctx, &nodes.ModelSchedulingConfig{ModelName: "drain-model", MinReplicas: 1})).To(Succeed())
		Expect(registry.SetNodeModel(ctx, n1.ID, "drain-model", 0, "loaded", "n1:6000", 0)).To(Succeed())

		// The worker of n1 answers the unload of the drained replica.
		var unloaded atomic.Int32
		sub, err := infra.NC.SubscribeReply(messaging.SubjectNodeModelUnload(n1.ID), func(data []byte, reply func([]byte)) {
			unloaded.Add(1)
			out, _ := json.Marshal(workerctl.ModelUnloadReply{Success: true})
			reply(out)
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(sub.Unsubscribe)
		FlushNATS(infra.NC)

		scheduler := &slowScheduler{registry: registry, target: n2, delay: 2 * time.Second}
		rc := nodes.NewReplicaReconciler(nodes.ReplicaReconcilerOptions{
			Registry:  registry,
			Scheduler: scheduler,
			Unloader:  nodes.NewRemoteUnloaderAdapter(registry, infra.NC, time.Minute, time.Minute),
			DB:        db,
			Interval:  200 * time.Millisecond,
		})

		// Sample the loaded replicas of the model during the whole drain.
		var (
			mu      sync.Mutex
			minSeen = 1 << 30
		)
		sampleCtx, stopSampling := context.WithCancel(ctx)
		sampled := make(chan struct{})
		go func() {
			defer close(sampled)
			for sampleCtx.Err() == nil {
				var n int64
				if db.Model(&nodes.NodeModel{}).Where("model_name = ? AND state = ?", "drain-model", "loaded").Count(&n).Error == nil {
					mu.Lock()
					minSeen = min(minSeen, int(n))
					mu.Unlock()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()

		res, err := registry.DrainNode(ctx, n1.ID, nodes.DrainOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Immediate).To(BeFalse())

		runCtx, stopRun := context.WithCancel(ctx)
		go rc.Run(runCtx)
		DeferCleanup(stopRun)

		Eventually(func() []string {
			var rows []nodes.NodeModel
			db.Where("model_name = ?", "drain-model").Find(&rows)
			ids := []string{}
			for _, r := range rows {
				ids = append(ids, r.NodeID)
			}
			return ids
		}, 30*time.Second, 100*time.Millisecond).Should(ConsistOf(n2.ID))
		stopSampling()
		<-sampled

		mu.Lock()
		defer mu.Unlock()
		Expect(minSeen).To(BeNumerically(">=", 1), "the model never had zero loaded replicas")
		Expect(scheduler.calls.Load()).To(BeNumerically(">=", 1))
		Eventually(unloaded.Load, 10*time.Second).Should(BeNumerically(">=", 1), "the drained replica's backend is stopped")

		node, err := registry.GetWithExtras(ctx, n1.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.Status).To(Equal(nodes.StatusDraining))
		Expect(node.LeavingReplicas).To(BeZero(), "an external tool may stop the worker now")
	})
})
