package nodes

import (
	"context"
	"errors"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

var _ = Describe("Node drain", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	register := func(name string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 4}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		return node
	}
	loaded := func(node *BackendNode, model string, replica int) {
		Expect(registry.SetNodeModel(ctx, node.ID, model, replica, "loaded", node.Name+":6000", 0)).To(Succeed())
	}
	rowsOn := func(node *BackendNode) []NodeModel {
		rows, err := registry.GetNodeModels(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		return rows
	}
	statusOf := func(node *BackendNode) *BackendNode {
		got, err := registry.Get(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		return got
	}

	Describe("DrainNode", func() {
		It("keeps the replicas of a healthy node and sets a deadline", func() {
			node := register("drain-keep")
			loaded(node, "m", 0)

			before := time.Now()
			res, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Immediate).To(BeFalse())
			Expect(res.PreviousStatus).To(Equal(StatusHealthy))
			Expect(res.Deadline).ToNot(BeNil())
			Expect(*res.Deadline).To(BeTemporally("~", before.Add(DefaultDrainTimeout), 5*time.Second))

			got := statusOf(node)
			Expect(got.Status).To(Equal(StatusDraining))
			Expect(got.DrainDeadline).ToNot(BeNil())
			Expect(rowsOn(node)).To(HaveLen(1), "the replica serves until it is replaced")
		})

		It("uses the configured default deadline and the timeout of the request", func() {
			a, b := register("drain-default"), register("drain-explicit")
			registry.SetDrainTimeout(time.Minute)

			res, err := registry.DrainNode(ctx, a.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(*res.Deadline).To(BeTemporally("~", time.Now().Add(time.Minute), 5*time.Second))

			res, err = registry.DrainNode(ctx, b.ID, DrainOptions{Timeout: time.Hour})
			Expect(err).ToNot(HaveOccurred())
			Expect(*res.Deadline).To(BeTemporally("~", time.Now().Add(time.Hour), 5*time.Second))
		})

		It("keeps the first deadline when the node is drained again", func() {
			node := register("drain-twice")
			first, err := registry.DrainNode(ctx, node.ID, DrainOptions{Timeout: time.Minute})
			Expect(err).ToNot(HaveOccurred())
			second, err := registry.DrainNode(ctx, node.ID, DrainOptions{Timeout: time.Hour})
			Expect(err).ToNot(HaveOccurred())
			Expect(second.PreviousStatus).To(Equal(StatusDraining))
			Expect(second.Deadline.Unix()).To(Equal(first.Deadline.Unix()))
		})

		It("removes the replicas at once when forced", func() {
			node := register("drain-force")
			loaded(node, "m", 0)
			var hooks []string
			registry.AddReplicaRemovedHook(func(model, _ string, _ int) { hooks = append(hooks, model) })

			res, err := registry.DrainNode(ctx, node.ID, DrainOptions{Force: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Immediate).To(BeTrue())
			Expect(res.Deadline).To(BeNil())
			Expect(statusOf(node).Status).To(Equal(StatusDraining))
			Expect(rowsOn(node)).To(BeEmpty())
			Expect(hooks).To(ConsistOf("m"))
		})

		It("forces a drain that already waits", func() {
			node := register("drain-then-force")
			loaded(node, "m", 0)
			_, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.MarkDraining(ctx, node.ID)).To(Succeed())
			Expect(rowsOn(node)).To(BeEmpty())
			Expect(statusOf(node).DrainDeadline).To(BeNil())
		})

		It("drains an unhealthy node at once: there is nothing to wait for", func() {
			node := register("drain-unhealthy")
			loaded(node, "m", 0)
			Expect(registry.MarkUnhealthy(ctx, node.ID)).To(Succeed())

			res, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Immediate).To(BeTrue())
			Expect(res.PreviousStatus).To(Equal(StatusUnhealthy))
			Expect(statusOf(node).Status).To(Equal(StatusDraining))
			Expect(rowsOn(node)).To(BeEmpty())
		})

		It("drains an offline node", func() {
			node := register("drain-offline")
			Expect(registry.MarkOffline(ctx, node.ID)).To(Succeed())
			res, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Immediate).To(BeTrue())
			Expect(statusOf(node).Status).To(Equal(StatusDraining))
		})

		It("refuses a pending node and reports a missing one", func() {
			pending := &BackendNode{Name: "drain-pending", NodeType: NodeTypeBackend, Address: "p:1"}
			Expect(registry.Register(ctx, pending, false)).To(Succeed())
			_, err := registry.DrainNode(ctx, pending.ID, DrainOptions{})
			Expect(errors.Is(err, ErrNodeStatusConflict)).To(BeTrue())
			Expect(statusOf(pending).Status).To(Equal(StatusPending))

			_, err = registry.DrainNode(ctx, "missing", DrainOptions{})
			Expect(errors.Is(err, ErrNodeNotFound)).To(BeTrue())
		})
	})

	It("is the same drain on every frontend", func() {
		node := register("two-frontends")
		loaded(node, "m", 0)
		other, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		res, err := registry.DrainNode(ctx, node.ID, DrainOptions{Timeout: time.Hour})
		Expect(err).ToNot(HaveOccurred())

		seen, err := other.GetWithExtras(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(seen.Status).To(Equal(StatusDraining))
		Expect(seen.DrainDeadline.Unix()).To(Equal(res.Deadline.Unix()))
		Expect(seen.LeavingReplicas).To(Equal(1))
		got, _, err := other.FindAndLockDrainingReplica(ctx, "m", nil, -1)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.ID).To(Equal(node.ID))

		Expect(other.ResumeNode(ctx, node.ID)).To(Succeed())
		Expect(statusOf(node).Status).To(Equal(StatusHealthy), "a resume through the other frontend ends it here")
	})

	Describe("ending a drain", func() {
		It("resume makes the node healthy and clears the deadline", func() {
			node := register("drain-resume")
			_, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.ResumeNode(ctx, node.ID)).To(Succeed())
			got := statusOf(node)
			Expect(got.Status).To(Equal(StatusHealthy))
			Expect(got.DrainDeadline).To(BeNil())
		})

		It("a worker that registers again ends the drain", func() {
			node := register("drain-reregister")
			_, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			again := &BackendNode{Name: "drain-reregister", NodeType: NodeTypeBackend, Address: node.Address}
			Expect(registry.Register(ctx, again, true)).To(Succeed())
			got := statusOf(node)
			Expect(got.Status).To(Equal(StatusHealthy))
			Expect(got.DrainDeadline).To(BeNil())
		})

		It("a transient route error does not undo a drain", func() {
			node := register("drain-unhealthy-later")
			_, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.MarkUnhealthy(ctx, node.ID)).To(Succeed())
			Expect(statusOf(node).Status).To(Equal(StatusDraining))
			Expect(registry.MarkUnhealthy(ctx, "missing")).ToNot(Succeed())
		})

		It("release removes the replicas only while the node drains", func() {
			node := register("drain-release")
			loaded(node, "m", 0)
			removed, err := registry.ReleaseDrainingNode(ctx, node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(removed).To(BeEmpty(), "a healthy node is not released")
			Expect(rowsOn(node)).To(HaveLen(1))

			_, err = registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())
			extras, err := registry.GetWithExtras(ctx, node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(extras.LeavingReplicas).To(Equal(1))
			list, err := registry.ListWithExtras(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(ContainElement(HaveField("LeavingReplicas", 1)))

			removed, err = registry.ReleaseDrainingNode(ctx, node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(removed).To(HaveLen(1))
			Expect(rowsOn(node)).To(BeEmpty())
		})
	})

	Describe("routing and placement", func() {
		It("places nothing on a draining node but keeps routing to its replicas", func() {
			draining, healthy := register("route-draining"), register("route-healthy")
			loaded(draining, "m", 0)
			_, err := registry.DrainNode(ctx, draining.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())

			candidates, err := registry.FindNodesBySelector(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(extractNodeIDs(candidates)).To(ConsistOf(healthy.ID))

			_, _, err = registry.FindAndLockNodeWithModel(ctx, "m", nil, nil)
			Expect(err).To(HaveOccurred(), "the healthy pass sees no replica")
			node, _, err := registry.FindAndLockDrainingReplica(ctx, "m", nil, -1)
			Expect(err).ToNot(HaveOccurred())
			Expect(node.ID).To(Equal(draining.ID))
			Expect(rowsOn(draining)[0].InFlight).To(Equal(1), "the pick reserves the replica like a healthy one")

			nodes, err := registry.FindNodesWithModel(ctx, "m")
			Expect(err).ToNot(HaveOccurred())
			Expect(extractNodeIDs(nodes)).To(ConsistOf(draining.ID), "an unload still reaches the draining replica")

			staying, err := registry.CountStayingReplicas(ctx, "m", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(staying).To(BeZero())
		})

		It("matches the selector of the rule on a draining node", func() {
			node := register("route-selector")
			Expect(registry.SetNodeLabel(ctx, node.ID, "tier", "gpu")).To(Succeed())
			loaded(node, "m", 0)
			_, err := registry.DrainNode(ctx, node.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())

			_, _, err = registry.FindAndLockDrainingReplica(ctx, "m", map[string]string{"tier": "cpu"}, -1)
			Expect(err).To(HaveOccurred())
			got, _, err := registry.FindAndLockDrainingReplica(ctx, "m", map[string]string{"tier": "gpu"}, -1)
			Expect(err).ToNot(HaveOccurred())
			Expect(got.ID).To(Equal(node.ID))
		})
	})
	Describe("the router", func() {
		var unloader *fakeUnloader
		newRouter := func() *SmartRouter {
			unloader = &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001"}}
			backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
			return NewSmartRouter(registry, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}, DB: db})
		}

		It("routes to a draining replica when no healthy node has the model", func() {
			draining := register("router-draining")
			register("router-other")
			loaded(draining, "m", 0)
			_, err := registry.DrainNode(ctx, draining.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())

			res, err := newRouter().Route(ctx, "m", "m.gguf", "llama-cpp", "", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Node.ID).To(Equal(draining.ID))
			Expect(unloader.installCalls).To(BeEmpty(), "no new load while the draining replica serves")
		})

		It("prefers the replacement on a healthy node", func() {
			draining, healthy := register("router-draining2"), register("router-healthy2")
			loaded(draining, "m", 0)
			loaded(healthy, "m", 0)
			_, err := registry.DrainNode(ctx, draining.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())

			for range 3 {
				res, err := newRouter().Route(ctx, "m", "m.gguf", "llama-cpp", "", nil, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Node.ID).To(Equal(healthy.ID))
				// The request ends, so the healthy replica is idle again.
				res.Release()
			}
		})

		It("drains a node whose worker refuses a load because it shuts down", func() {
			node := register("router-stopping")
			blob, err := proto.Marshal(&pb.ModelOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.UpsertModelLoadInfo(ctx, "m", "llama-cpp", blob)).To(Succeed())
			router := newRouter()
			unloader.installReply = &workerctl.BackendInstallReply{Success: false, ShuttingDown: true, Error: workerctl.ErrWorkerShuttingDownMsg}

			_, err = router.ScheduleAndLoadModel(ctx, "m", nil)
			Expect(err).To(MatchError(ErrNodeShuttingDown))
			Expect(err).To(MatchError(ErrNoAvailableNodes), "a client retries it")
			Expect(statusOf(node).Status).To(Equal(StatusDraining), "placement skips the node at once")
		})

		It("serves a draining replica when the rule matches no healthy node", func() {
			draining := register("router-gpu")
			Expect(registry.SetNodeLabel(ctx, draining.ID, "tier", "gpu")).To(Succeed())
			Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "m", NodeSelector: `{"tier":"gpu"}`})).To(Succeed())
			loaded(draining, "m", 0)
			_, err := registry.DrainNode(ctx, draining.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())

			res, err := newRouter().Route(ctx, "m", "m.gguf", "llama-cpp", "", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Node.ID).To(Equal(draining.ID))
		})

		It("serves the draining replica, not a stray replica on a healthy node the rule excludes", func() {
			draining, stray := register("router-gpu2"), register("router-cpu2")
			Expect(registry.SetNodeLabel(ctx, draining.ID, "tier", "gpu")).To(Succeed())
			Expect(registry.SetNodeLabel(ctx, stray.ID, "tier", "cpu")).To(Succeed())
			Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "m", NodeSelector: `{"tier":"gpu"}`})).To(Succeed())
			loaded(draining, "m", 0)
			loaded(stray, "m", 0)
			_, err := registry.DrainNode(ctx, draining.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())

			res, err := newRouter().Route(ctx, "m", "m.gguf", "llama-cpp", "", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Node.ID).To(Equal(draining.ID))
			Expect(rowsOn(stray)[0].InFlight).To(BeZero(), "the stray replica keeps no reservation")
		})

		It("routes to the draining replica while the healthy replica is busy", func() {
			draining, healthy := register("router-draining3"), register("router-healthy3")
			loaded(draining, "m", 0)
			loaded(healthy, "m", 0)
			Expect(registry.IncrementInFlight(ctx, healthy.ID, "m", 0)).To(Succeed())
			_, err := registry.DrainNode(ctx, draining.ID, DrainOptions{})
			Expect(err).ToNot(HaveOccurred())

			res, err := newRouter().Route(ctx, "m", "m.gguf", "llama-cpp", "", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Node.ID).To(Equal(draining.ID), "the healthy replica is saturated: the draining one serves")
			Expect(rowsOn(healthy)[0].InFlight).To(Equal(1), "the reservation taken on the busy replica is released")

			// Both now run one request: the healthy replica wins the tie.
			res, err = newRouter().Route(ctx, "m", "m.gguf", "llama-cpp", "", nil, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Node.ID).To(Equal(healthy.ID))
		})
	})
	Describe("the health monitor", func() {
		It("removes the replicas of a draining node that stopped answering", func() {
			alive, dead := register("hm-alive"), register("hm-dead")
			loaded(alive, "m", 0)
			loaded(dead, "m", 0)
			for _, n := range []*BackendNode{alive, dead} {
				_, err := registry.DrainNode(ctx, n.ID, DrainOptions{})
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(db.Model(&BackendNode{}).Where("id = ?", dead.ID).
				Update("last_heartbeat", time.Now().Add(-time.Hour)).Error).To(Succeed())

			hm := NewHealthMonitor(registry, nil, 15*time.Second, time.Minute, "", false)
			hm.doCheckAll(ctx)

			Expect(rowsOn(alive)).To(HaveLen(1), "a draining node that answers keeps serving")
			Expect(rowsOn(dead)).To(BeEmpty())
			Expect(statusOf(dead).Status).To(Equal(StatusDraining), "the drain stays the operator's")
		})
	})
})
