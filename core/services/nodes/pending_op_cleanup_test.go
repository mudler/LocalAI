package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// These specs reproduce the distributed "pending ops behind dead nodes leak
// forever" bug. ListDuePendingBackendOps only returns rows whose node is
// StatusHealthy, so an op queued against a node that goes offline (heartbeat
// stale) or draining (admin action) is never retried, never aged out, and
// never deleted. On a live cluster these rows sat at attempts=0 indefinitely
// and kept the UI operation alive. DeleteStalePendingBackendOps garbage-collects
// them: draining nodes immediately (models already purged), offline nodes only
// after a grace window so a brief heartbeat blip does not nuke in-flight work.
var _ = Describe("DeleteStalePendingBackendOps", func() {
	var (
		registry *NodeRegistry
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
		ctx = context.Background()
	})

	// registerBackend registers an auto-approved backend node and returns its ID.
	registerBackend := func(name, address string) string {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: address}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		return fetched.ID
	}

	// setHeartbeat forces a node's last_heartbeat (Register/MarkOffline leave it
	// at "now"; we age it to simulate a node that went silent a while ago).
	setHeartbeat := func(nodeID string, t time.Time) {
		Expect(registry.db.WithContext(ctx).Model(&BackendNode{}).
			Where("id = ?", nodeID).
			Update("last_heartbeat", t).Error).To(Succeed())
	}

	pendingCountFor := func(nodeID string) int64 {
		var n int64
		Expect(registry.db.WithContext(ctx).Model(&PendingBackendOp{}).
			Where("node_id = ?", nodeID).Count(&n).Error).To(Succeed())
		return n
	}

	It("clears ops behind an offline node whose heartbeat is past the grace window", func() {
		dead := registerBackend("nvidia-thor", "10.0.0.9:50051")
		Expect(registry.UpsertPendingBackendOp(ctx, dead, "llama-cpp-development", OpBackendInstall, nil)).To(Succeed())
		Expect(registry.MarkOffline(ctx, dead)).To(Succeed())
		setHeartbeat(dead, time.Now().Add(-1*time.Hour))

		removed, err := registry.DeleteStalePendingBackendOps(ctx, 10*time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(Equal(int64(1)))
		Expect(pendingCountFor(dead)).To(Equal(int64(0)))
	})

	It("clears ops behind a draining node immediately, even with a fresh heartbeat", func() {
		// Mirrors the live mac-mini-m4 case: draining but still heartbeating.
		drain := registerBackend("mac-mini-m4", "10.0.0.3:50051")
		Expect(registry.UpsertPendingBackendOp(ctx, drain, "llama-cpp-development", OpBackendInstall, nil)).To(Succeed())
		Expect(registry.MarkDraining(ctx, drain)).To(Succeed())
		setHeartbeat(drain, time.Now()) // fresh heartbeat

		removed, err := registry.DeleteStalePendingBackendOps(ctx, 10*time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(Equal(int64(1)))
		Expect(pendingCountFor(drain)).To(Equal(int64(0)))
	})

	It("keeps ops behind a node that only just went offline (within grace)", func() {
		blip := registerBackend("agx-orin", "10.0.0.4:50051")
		Expect(registry.UpsertPendingBackendOp(ctx, blip, "parakeet-cpp-development", OpBackendInstall, nil)).To(Succeed())
		Expect(registry.MarkOffline(ctx, blip)).To(Succeed())
		setHeartbeat(blip, time.Now().Add(-1*time.Minute)) // gone only 1m, grace 10m

		removed, err := registry.DeleteStalePendingBackendOps(ctx, 10*time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(Equal(int64(0)))
		Expect(pendingCountFor(blip)).To(Equal(int64(1)))
	})

	It("keeps ops behind a healthy node", func() {
		healthy := registerBackend("dgx-spark", "10.0.0.1:50051")
		Expect(registry.UpsertPendingBackendOp(ctx, healthy, "llama-cpp-development", OpBackendUpgrade, nil)).To(Succeed())

		removed, err := registry.DeleteStalePendingBackendOps(ctx, 10*time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(Equal(int64(0)))
		Expect(pendingCountFor(healthy)).To(Equal(int64(1)))
	})
})
