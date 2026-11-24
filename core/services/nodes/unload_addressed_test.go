// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// addrUnloader records the unloads that name a replica's address. The worker
// frees exactly that process; a request that names only the model frees nothing.
type addrUnloader struct {
	*fakeUnloader
	mu      sync.Mutex
	unloads []NodeModel
}

func (u *addrUnloader) UnloadReplica(_ string, replica NodeModel) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.unloads = append(u.unloads, replica)
	return nil
}

func (u *addrUnloader) addressed() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []string
	for _, r := range u.unloads {
		out = append(out, r.ModelName+"@"+r.Address)
	}
	return out
}

// Both callers delete the replica row before they unload. The unload has to
// carry the address they read first, or the backend keeps the model in memory
// with nothing in the registry that points at it.
var _ = Describe("Unloading a replica that was just removed from the registry", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		ctx      context.Context
		unloader *addrUnloader
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx = context.Background()
		unloader = &addrUnloader{fakeUnloader: &fakeUnloader{}}
	})

	seed := func(name, model, addr string, idle time.Duration) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		Expect(db.Create(&NodeModel{ID: name + "-" + model, NodeID: node.ID, ModelName: model, Address: addr,
			State: "loaded", LastUsed: time.Now().Add(-idle), UpdatedAt: time.Now()}).Error).To(Succeed())
		return node
	}

	It("sends an addressed unload on LRU eviction", func() {
		node := seed("evict-node", "evicted-model", "10.0.0.9:9001", time.Hour)
		router := NewSmartRouter(registry, SmartRouterOptions{DB: db, Unloader: unloader})

		got, err := router.evictLRUAndFreeNodeFrom(ctx, nil)

		Expect(err).ToNot(HaveOccurred())
		Expect(got.ID).To(Equal(node.ID))
		Expect(unloader.addressed()).To(ConsistOf("evicted-model@10.0.0.9:9001"))
		unloader.fakeUnloader.mu.Lock()
		defer unloader.fakeUnloader.mu.Unlock()
		Expect(unloader.fakeUnloader.unloadCalls).To(BeEmpty(), "no name-only unload")
	})

	It("sends an addressed unload on reconciler scale-down", func() {
		n1 := seed("down-1", "scaled-model", "10.0.0.1:9001", 10*time.Minute)
		n2 := seed("down-2", "scaled-model", "10.0.0.2:9002", 10*time.Minute)
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "scaled-model", MinReplicas: 1, MaxReplicas: 4})).To(Succeed())
		_ = n1
		_ = n2
		rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, Unloader: unloader, DB: db, ScaleDownDelay: time.Minute})

		rc.reconcile(ctx)

		Expect(unloader.addressed()).To(HaveLen(1), "one of two replicas goes")
		Expect(unloader.addressed()[0]).To(MatchRegexp(`^scaled-model@10\.0\.0\.[12]:900[12]$`))
		var left int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", "scaled-model").Count(&left).Error).To(Succeed())
		Expect(left).To(Equal(int64(1)))
	})
})
