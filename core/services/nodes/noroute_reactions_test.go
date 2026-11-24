package nodes

import (
	"context"
	"runtime"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/system"
)

// These specs pin the reactions the seams contract allows on ErrNoRoute: the
// status-only MarkUnhealthy. An upgrade has no fallback to a legacy install.
// Each one drives the real caller with a scripted no-responders reply and reads
// the outcome back from the registry or the recorded requests, so a change to
// the reaction fails here instead of silently widening or dropping it.
var _ = Describe("ErrNoRoute reactions", func() {
	var (
		registry *NodeRegistry
		mc       *scriptedMessagingClient
		adapter  *RemoteUnloaderAdapter
		ctx      context.Context
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db := testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		mc = newScriptedMessagingClient()
		adapter = NewRemoteUnloaderAdapter(nil, mc, 3*time.Minute, 15*time.Minute)
		ctx = context.Background()
	})

	registerHealthy := func(name string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.Get(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(fetched.Status).To(Equal(StatusHealthy))
		return fetched
	}

	statusOf := func(nodeID string) string {
		n, err := registry.Get(ctx, nodeID)
		Expect(err).ToNot(HaveOccurred())
		return n.Status
	}

	pendingRow := func(nodeID, op string) (PendingBackendOp, bool) {
		var rows []PendingBackendOp
		Expect(registry.db.WithContext(ctx).Where("node_id = ? AND op = ?", nodeID, op).Find(&rows).Error).To(Succeed())
		if len(rows) == 0 {
			return PendingBackendOp{}, false
		}
		return rows[0], true
	}

	Describe("reconciler pending-op drain", func() {
		var rc *ReplicaReconciler

		BeforeEach(func() {
			rc = NewReplicaReconciler(ReplicaReconcilerOptions{
				Registry: registry,
				Adapter:  adapter,
				DB:       registry.db,
			})
		})

		It("keeps a queued upgrade and marks the node unhealthy when the upgrade has no route", func() {
			n := registerHealthy("worker-old")
			Expect(registry.UpsertPendingBackendOp(ctx, n.ID, "vllm", OpBackendUpgrade, []byte("[]"))).To(Succeed())
			mc.scriptNoResponders(messaging.SubjectNodeBackendUpgrade(n.ID))

			rc.drainPendingBackendOps(ctx)

			mc.mu.Lock()
			for _, call := range mc.calls {
				Expect(call.Subject).ToNot(Equal(messaging.SubjectNodeBackendInstall(n.ID)), "no legacy install is sent")
			}
			mc.mu.Unlock()
			row, queued := pendingRow(n.ID, OpBackendUpgrade)
			Expect(queued).To(BeTrue(), "the row waits for a node that serves the upgrade")
			Expect(row.Attempts).To(Equal(1))
			Expect(statusOf(n.ID)).To(Equal(StatusUnhealthy))
		})

		It("drops a queued upgrade that the worker rolled back, and keeps one that failed otherwise", func() {
			rolled := registerHealthy("worker-rolled")
			failed := registerHealthy("worker-failed")
			Expect(registry.UpsertPendingBackendOp(ctx, rolled.ID, "vllm", OpBackendUpgrade, []byte("[]"))).To(Succeed())
			Expect(registry.UpsertPendingBackendOp(ctx, failed.ID, "vllm", OpBackendUpgrade, []byte("[]"))).To(Succeed())
			mc.scriptReply(messaging.SubjectNodeBackendUpgrade(rolled.ID),
				workerctl.BackendUpgradeReply{Success: false, Code: "start", RolledBack: true, Error: "the new build did not start"})
			mc.scriptReply(messaging.SubjectNodeBackendUpgrade(failed.ID),
				workerctl.BackendUpgradeReply{Success: false, Code: "download", Error: "registry unreachable"})

			rc.drainPendingBackendOps(ctx)

			_, queued := pendingRow(rolled.ID, OpBackendUpgrade)
			Expect(queued).To(BeFalse(), "a retry would download the same build and fail the same way")
			row, queued := pendingRow(failed.ID, OpBackendUpgrade)
			Expect(queued).To(BeTrue())
			Expect(row.Attempts).To(Equal(1))
			Expect(statusOf(rolled.ID)).To(Equal(StatusHealthy), "the worker answered: it is present")
		})

		It("marks the node unhealthy when an op has no route, and still counts the attempt", func() {
			n := registerHealthy("worker-gone")
			Expect(registry.UpsertPendingBackendOp(ctx, n.ID, "vllm", OpBackendDelete, nil)).To(Succeed())
			mc.scriptNoResponders(messaging.SubjectNodeBackendDelete(n.ID))

			rc.drainPendingBackendOps(ctx)

			Expect(statusOf(n.ID)).To(Equal(StatusUnhealthy))
			row, queued := pendingRow(n.ID, OpBackendDelete)
			Expect(queued).To(BeTrue())
			Expect(row.Attempts).To(Equal(1))
		})

		It("leaves the node healthy when the op times out", func() {
			n := registerHealthy("worker-slow")
			Expect(registry.UpsertPendingBackendOp(ctx, n.ID, "vllm", OpBackendDelete, nil)).To(Succeed())
			mc.scriptErr(messaging.SubjectNodeBackendDelete(n.ID), nats.ErrTimeout)

			rc.drainPendingBackendOps(ctx)

			Expect(statusOf(n.ID)).To(Equal(StatusHealthy))
			row, queued := pendingRow(n.ID, OpBackendDelete)
			Expect(queued).To(BeTrue())
			Expect(row.Attempts).To(Equal(1))
		})
	})

	Describe("DistributedBackendManager fan-out", func() {
		var mgr *DistributedBackendManager

		BeforeEach(func() {
			mgr = &DistributedBackendManager{
				local:       stubLocalBackendManager{},
				adapter:     adapter,
				registry:    registry,
				systemState: &system.SystemState{},
			}
		})

		It("marks a node with no route unhealthy and leaves an answering node healthy", func() {
			gone := registerHealthy("worker-gone")
			answering := registerHealthy("worker-answering")
			mc.scriptNoResponders(messaging.SubjectNodeBackendDelete(gone.ID))
			mc.scriptReply(messaging.SubjectNodeBackendDelete(answering.ID),
				workerctl.BackendDeleteReply{Success: false, Error: "backend not installed"})

			Expect(mgr.DeleteBackend("vllm")).ToNot(Succeed())

			Expect(statusOf(gone.ID)).To(Equal(StatusUnhealthy))
			Expect(statusOf(answering.ID)).To(Equal(StatusHealthy))
		})
	})
})
