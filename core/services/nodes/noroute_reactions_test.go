package nodes

import (
	"context"
	"encoding/json"
	"runtime"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// These specs pin the reactions the seams contract allows on ErrNoRoute: the
// status-only MarkUnhealthy, and the legacy install fallback for an upgrade.
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

		It("falls back to the legacy forced install when the upgrade has no route", func() {
			n := registerHealthy("worker-old")
			Expect(registry.UpsertPendingBackendOp(ctx, n.ID, "vllm", OpBackendUpgrade, []byte("[]"))).To(Succeed())

			mc.scriptNoResponders(messaging.SubjectNodeBackendUpgrade(n.ID))
			mc.scriptReplyMatching(messaging.SubjectNodeBackendInstall(n.ID),
				func(req workerctl.BackendInstallRequest) bool { return req.Force },
				workerctl.BackendInstallReply{Success: true, Address: "10.0.0.1:50100"})

			rc.drainPendingBackendOps(ctx)

			var forcedInstalls int
			mc.mu.Lock()
			for _, call := range mc.calls {
				if call.Subject != messaging.SubjectNodeBackendInstall(n.ID) {
					continue
				}
				var req workerctl.BackendInstallRequest
				Expect(json.Unmarshal(call.Data, &req)).To(Succeed())
				if req.Force && req.Backend == "vllm" {
					forcedInstalls++
				}
			}
			mc.mu.Unlock()
			Expect(forcedInstalls).To(Equal(1))

			_, stillQueued := pendingRow(n.ID, OpBackendUpgrade)
			Expect(stillQueued).To(BeFalse(), "a successful fallback drains the row")
			Expect(statusOf(n.ID)).To(Equal(StatusHealthy), "an old worker that answered the fallback is not unhealthy")
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
				local:    stubLocalBackendManager{},
				adapter:  adapter,
				registry: registry,
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
