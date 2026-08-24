package nodes

import (
	"context"
	"fmt"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// When no node the selector allows has a free slot, scheduling falls back to
// evicting the globally least-recently-used model. That eviction knew nothing
// about the selector, so a model pinned to one class of hardware would evict an
// unrelated model from a node it is not allowed to run on, and then be placed
// there. Two models lose: the pinned one runs on the wrong hardware, and the
// evicted one is dropped for nothing and has to reload elsewhere.
var _ = Describe("Eviction under a node selector", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		router   *SmartRouter
		ctx      context.Context
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		router = NewSmartRouter(registry, SmartRouterOptions{DB: db})
		ctx = context.Background()
	})

	register := func(name string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		return fetched
	}

	rowID := 0
	seed := func(node *BackendNode, model string, idleFor time.Duration, inFlight int) {
		rowID++
		Expect(db.Create(&NodeModel{
			ID: fmt.Sprintf("row-%d", rowID), NodeID: node.ID, ModelName: model,
			Address: node.Address, State: "loaded", InFlight: inFlight,
			LastUsed: time.Now().Add(-idleFor), UpdatedAt: time.Now(),
		}).Error).To(Succeed())
	}
	seedLoaded := func(node *BackendNode, model string, idleFor time.Duration) {
		seed(node, model, idleFor, 0)
	}

	rowExists := func(model string) bool {
		var n int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", model).Count(&n).Error).To(Succeed())
		return n > 0
	}

	It("does not evict from a node the selector excludes", func() {
		allowed := register("allowed-node")
		excluded := register("excluded-node")
		// The only eviction candidate sits on the excluded node and is the
		// global LRU, so an unconstrained eviction would take it.
		seedLoaded(excluded, "innocent-bystander", time.Hour)
		// In-flight, so it is not an eviction candidate: the allowed node has
		// nothing that can be freed.
		seed(allowed, "busy-here", time.Minute, 1)

		_, err := router.evictLRUAndFreeNodeFrom(ctx, []string{allowed.ID})

		Expect(err).To(HaveOccurred(), "no eviction candidate exists on an allowed node")
		Expect(rowExists("innocent-bystander")).To(BeTrue(),
			"a model on a node the selector excludes must not be evicted to make room")
	})

	It("evicts the LRU among the allowed nodes only", func() {
		allowed := register("allowed-node")
		excluded := register("excluded-node")
		seedLoaded(excluded, "older-elsewhere", 2*time.Hour)
		seedLoaded(allowed, "newer-but-allowed", time.Hour)

		node, err := router.evictLRUAndFreeNodeFrom(ctx, []string{allowed.ID})

		Expect(err).ToNot(HaveOccurred())
		Expect(node.ID).To(Equal(allowed.ID))
		Expect(rowExists("newer-but-allowed")).To(BeFalse())
		Expect(rowExists("older-elsewhere")).To(BeTrue())
	})

	It("keeps evicting globally when the model has no selector", func() {
		a := register("node-a")
		seedLoaded(a, "anything", time.Hour)

		node, err := router.evictLRUAndFreeNodeFrom(ctx, nil)

		Expect(err).ToNot(HaveOccurred())
		Expect(node.ID).To(Equal(a.ID))
		Expect(rowExists("anything")).To(BeFalse())
	})
})
