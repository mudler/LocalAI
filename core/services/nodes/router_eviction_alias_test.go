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

// A replica floor protects a model from LRU eviction. The guard that enforces
// it matches a scheduling rule against the loaded model's name, so a rule keyed
// by an alias has to reach the model the alias points at. Without that, the
// router evicts under the floor and the reconciler reloads on its next tick,
// which is the replica flapping this floor exists to prevent.
var _ = Describe("Eviction against an alias-keyed replica floor", func() {
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
		registry.SetAliasResolver(&fakeAliasResolver{aliases: map[string]string{"production": "qwen3"}})
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
	seedLoaded := func(node *BackendNode, model string, idleFor time.Duration) {
		rowID++
		Expect(db.Create(&NodeModel{
			ID: fmt.Sprintf("alias-row-%d", rowID), NodeID: node.ID, ModelName: model,
			Address: node.Address, State: "loaded", InFlight: 0,
			LastUsed: time.Now().Add(-idleFor), UpdatedAt: time.Now(),
		}).Error).To(Succeed())
	}

	rowExists := func(model string) bool {
		var n int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", model).Count(&n).Error).To(Succeed())
		return n > 0
	}

	It("protects the target of an alias-keyed rule at its floor", func() {
		node := register("floor-node")
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "production", MinReplicas: 1})).To(Succeed())
		seedLoaded(node, "qwen3", time.Hour)

		_, err := router.evictLRUAndFreeNodeFrom(ctx, nil)

		Expect(err).To(HaveOccurred(), "the only candidate sits at its replica floor")
		Expect(rowExists("qwen3")).To(BeTrue())
	})

	It("still evicts the target above its floor", func() {
		n1 := register("floor-node-a")
		n2 := register("floor-node-b")
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "production", MinReplicas: 1})).To(Succeed())
		seedLoaded(n1, "qwen3", 2*time.Hour)
		seedLoaded(n2, "qwen3", time.Hour)

		_, err := router.evictLRUAndFreeNodeFrom(ctx, nil)

		Expect(err).ToNot(HaveOccurred())
		var remaining int64
		Expect(db.Model(&NodeModel{}).Where("model_name = ?", "qwen3").Count(&remaining).Error).To(Succeed())
		Expect(remaining).To(BeNumerically("==", 1))
	})

	It("stops protecting the old target once the alias is repointed", func() {
		node := register("floor-node-c")
		Expect(registry.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "production", MinReplicas: 1})).To(Succeed())
		seedLoaded(node, "qwen3", time.Hour)
		registry.SetAliasResolver(&fakeAliasResolver{aliases: map[string]string{"production": "llama4"}})
		Expect(registry.RefreshSchedulingTargets(ctx)).To(Succeed())

		_, err := router.evictLRUAndFreeNodeFrom(ctx, nil)

		Expect(err).ToNot(HaveOccurred())
		Expect(rowExists("qwen3")).To(BeFalse())
	})
})
