package distributed_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Node Backend Lifecycle (NATS-driven)", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		db       *gorm.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_lifecycle_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("NATS backend.install events", func() {
		It("should send backend.install request-reply to a specific node", func() {
			node := &nodes.BackendNode{
				Name: "gpu-node-1", Address: "h1:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Simulate worker subscribing to backend.install and replying success
			infra.NC.SubscribeReply(messaging.SubjectNodeBackendInstall(node.ID), func(data []byte, reply func([]byte)) {
				var req workerctl.BackendInstallRequest
				json.Unmarshal(data, &req)
				Expect(req.Backend).To(Equal("llama-cpp"))

				resp := workerctl.BackendInstallReply{Success: true}
				respData, _ := json.Marshal(resp)
				reply(respData)
			})

			FlushNATS(infra.NC)

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, 3*time.Minute, 15*time.Minute)
			installReply, err := adapter.InstallBackend(node.ID, "llama-cpp", "", "", "", "", "", 0, "", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(installReply.Success).To(BeTrue())
		})

		It("should propagate error from worker on failed install", func() {
			node := &nodes.BackendNode{
				Name: "fail-node", Address: "h1:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())

			// Simulate worker replying with error
			infra.NC.SubscribeReply(messaging.SubjectNodeBackendInstall(node.ID), func(data []byte, reply func([]byte)) {
				resp := workerctl.BackendInstallReply{Success: false, Error: "backend not found"}
				respData, _ := json.Marshal(resp)
				reply(respData)
			})

			FlushNATS(infra.NC)

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, 3*time.Minute, 15*time.Minute)
			installReply, err := adapter.InstallBackend(node.ID, "nonexistent", "", "", "", "", "", 0, "", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(installReply.Success).To(BeFalse())
			Expect(installReply.Error).To(ContainSubstring("backend not found"))
		})
	})

	Context("NATS backend.stop events (model unload)", func() {
		It("should send backend.stop to nodes hosting the model", func() {
			node := &nodes.BackendNode{
				Name: "gpu-node-2", Address: "h2:50051",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "whisper-large", 0, "loaded", "", 0)).To(Succeed())

			var stopReceived atomic.Int32
			sub, err := infra.NC.Subscribe(messaging.SubjectNodeBackendStop(node.ID), func(data []byte) {
				stopReceived.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			// Frontend calls UnloadRemoteModel (triggered by UI "Stop" or WatchDog)
			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, 3*time.Minute, 15*time.Minute)
			Expect(adapter.UnloadRemoteModel("whisper-large")).To(Succeed())

			Eventually(func() int32 { return stopReceived.Load() }, "5s").Should(Equal(int32(1)))

			// Model should be removed from registry
			nodesWithModel, _ := registry.FindNodesWithModel(context.Background(), "whisper-large")
			Expect(nodesWithModel).To(BeEmpty())
		})

		It("should send backend.stop to all nodes hosting the model", func() {
			node1 := &nodes.BackendNode{Name: "n1", Address: "h1:50051"}
			node2 := &nodes.BackendNode{Name: "n2", Address: "h2:50051"}
			registry.Register(context.Background(), node1, true)
			registry.Register(context.Background(), node2, true)
			registry.SetNodeModel(context.Background(), node1.ID, "shared-model", 0, "loaded", "", 0)
			registry.SetNodeModel(context.Background(), node2.ID, "shared-model", 0, "loaded", "", 0)

			var count atomic.Int32
			sub1, _ := infra.NC.Subscribe(messaging.SubjectNodeBackendStop(node1.ID), func(data []byte) {
				count.Add(1)
			})
			sub2, _ := infra.NC.Subscribe(messaging.SubjectNodeBackendStop(node2.ID), func(data []byte) {
				count.Add(1)
			})
			defer sub1.Unsubscribe()
			defer sub2.Unsubscribe()

			FlushNATS(infra.NC)

			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, 3*time.Minute, 15*time.Minute)
			adapter.UnloadRemoteModel("shared-model")

			Eventually(func() int32 { return count.Load() }, "5s").Should(Equal(int32(2)))
		})

		It("should be no-op for models not on any node", func() {
			// Unloading is idempotent by contract: cleanup paths (model
			// deletion, config edits, watchdog eviction) legitimately run
			// against an already-unloaded model, and the watchdog's LRU
			// reclaimer only untracks a model when shutdown reports success.
			// Callers needing to tell "stopped it" from "nothing to stop" —
			// ShutdownModel, so it can answer 404 — use HasRemoteModel instead.
			// The same contract is pinned at unit level by "with no nodes
			// returns nil" in core/services/nodes/unloader_test.go; keep them
			// in step.
			adapter := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, 3*time.Minute, 15*time.Minute)
			Expect(adapter.UnloadRemoteModel("nonexistent-model")).To(Succeed())
		})
	})

	Context("NATS subject naming", func() {
		It("should generate correct backend lifecycle subjects", func() {
			Expect(messaging.SubjectNodeBackendInstall("node-abc")).To(Equal("nodes.node-abc.backend.install"))
			Expect(messaging.SubjectNodeBackendStop("node-abc")).To(Equal("nodes.node-abc.backend.stop"))
			Expect(messaging.SubjectNodeStop("node-abc")).To(Equal("nodes.node-abc.stop"))
		})
	})

	// Design note: LoadModel is a direct gRPC call to node.Address, NOT a NATS event.
	// NATS is used for backend.install (install + start process) and backend.stop.
	// The SmartRouter calls grpc.NewClient(node.Address).LoadModel() directly.
	//
	// Flow:
	// 1. NATS backend.install → worker installs backend + starts gRPC process
	// 2. SmartRouter.Route() → gRPC LoadModel(node.Address) directly
	// 3. [inference via gRPC]
	// 4. NATS backend.stop → worker stops gRPC process
})

// slowLoadLLM is a gRPC backend whose Load blocks until release is closed. It
// stands in for a node where a checkpoint load takes a very long time.
type slowLoadLLM struct {
	base.Base
	release <-chan struct{}
	started chan<- struct{}
}

func (s *slowLoadLLM) Load(_ *pb.ModelOptions) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-s.release
	return nil
}

// Load jobs are keyed by (model, node). A request-path load that is slow on
// one node must not stop the reconciler from loading the same model on
// another node: before the jobs were per node, the reconciler was refused
// with "already being loaded by another owner" until the slow load ended.
var _ = Describe("Load jobs per node", Label("Distributed"), func() {
	It("lets the reconciler load on node 2 while a slow load runs on node 1", func() {
		infra := SetupInfra("localai_load_job_per_node_test")
		db, err := gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(infra.Ctx)
		defer cancel()

		_, err = infra.NC.Conn().Subscribe("nodes.*.backend.install", func(msg *nats.Msg) {
			data, _ := json.Marshal(workerctl.BackendInstallReply{Success: true})
			_ = msg.Respond(data)
		})
		Expect(err).ToNot(HaveOccurred())
		// The scheduler checks that a node answers on the bus before it uses it.
		_, err = infra.NC.Conn().Subscribe("nodes.*.models.running", func(msg *nats.Msg) {
			data, _ := json.Marshal(workerctl.ModelsRunningReply{})
			_ = msg.Respond(data)
		})
		Expect(err).ToNot(HaveOccurred())
		FlushNATS(infra.NC)

		release := make(chan struct{})
		slowStarted := make(chan struct{}, 1)
		slowAddr, stopSlow, err := startTestGRPCServer(grpcPkg.AIModel(&slowLoadLLM{release: release, started: slowStarted}))
		Expect(err).ToNot(HaveOccurred())
		fastAddr, stopFast, err := startTestGRPCServer(grpcPkg.AIModel(&trackingTestLLM{}))
		Expect(err).ToNot(HaveOccurred())
		// Stop the reconciler first, then let the slow load return, so that
		// the graceful stop of its server does not wait for it.
		defer func() {
			cancel()
			close(release)
			stopSlow()
			stopFast()
		}()

		const model = "model-x"
		blob, err := proto.Marshal(&pb.ModelOptions{Model: "model-x.gguf"})
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpsertModelLoadInfo(ctx, model, "llama-cpp", blob)).To(Succeed())

		// Only node 1 exists when the first request arrives, so the request
		// starts its load there.
		node1 := &nodes.BackendNode{Name: "node-1", NodeType: nodes.NodeTypeBackend, Address: slowAddr}
		Expect(registry.Register(ctx, node1, true)).To(Succeed())

		unloader := nodes.NewRemoteUnloaderAdapter(registry, infra.NC, 3*time.Minute, 15*time.Minute)
		router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{Unloader: unloader, DB: db})

		served := make(chan *nodes.RouteResult, 1)
		go func() {
			defer GinkgoRecover()
			res, err := router.Route(ctx, model, "model-x.gguf", "llama-cpp", "", &pb.ModelOptions{Model: "model-x.gguf"}, false)
			Expect(err).ToNot(HaveOccurred())
			served <- res
		}()
		Eventually(slowStarted, 30*time.Second).Should(Receive(), "the request load reached LoadModel on node 1")

		// Node 2 joins, and the model must keep two replicas.
		node2 := &nodes.BackendNode{Name: "node-2", NodeType: nodes.NodeTypeBackend, Address: fastAddr}
		Expect(registry.Register(ctx, node2, true)).To(Succeed())
		Expect(registry.SetModelScheduling(ctx, &nodes.ModelSchedulingConfig{ModelName: model, MinReplicas: 2})).To(Succeed())

		reconciler := nodes.NewReplicaReconciler(nodes.ReplicaReconcilerOptions{
			Registry: registry, Scheduler: router, Unloader: unloader, DB: db, Interval: 200 * time.Millisecond,
		})
		go reconciler.Run(ctx)

		Eventually(func() []string {
			found, _ := registry.FindNodesWithModel(ctx, model)
			ids := []string{}
			for _, n := range found {
				ids = append(ids, n.ID)
			}
			return ids
		}, 60*time.Second, 200*time.Millisecond).Should(ContainElement(node2.ID), "the reconciler loaded on node 2")

		slow, err := registry.GetLoadJobOn(ctx, model, node1.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(slow).ToNot(BeNil(), "the slow load on node 1 is still running")
		Expect(slow.State).ToNot(Equal(nodes.LoadJobStateFailed))

		// The waiting request is served by the replica that became ready.
		var res *nodes.RouteResult
		Eventually(served, 30*time.Second).Should(Receive(&res))
		Expect(res.Node.ID).To(Equal(node2.ID))
		res.Release()
	})
})
