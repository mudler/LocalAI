package distributed_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("DistributedModelStore", Label("Distributed"), func() {
	var (
		infra      *TestInfra
		db         *gorm.DB
		registry   *nodes.NodeRegistry
		localStore *model.InMemoryModelStore
		dStore     *nodes.DistributedModelStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_dstore_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		localStore = model.NewInMemoryModelStore()
		dStore = nodes.NewDistributedModelStore(localStore, registry)
	})

	Context("Get", func() {
		It("returns model from local cache on hit", func() {
			expected := model.NewModel("local-model", "local:5000", nil)
			localStore.Set("local-model", expected)

			m, ok := dStore.Get("local-model")
			Expect(ok).To(BeTrue())
			Expect(m).To(Equal(expected))
		})

		It("returns (nil, false) when model is not in local cache", func() {
			m, ok := dStore.Get("ghost-model")
			Expect(ok).To(BeFalse())
			Expect(m).To(BeNil())
		})
	})

	Context("Set", func() {
		It("delegates to local store", func() {
			expected := model.NewModel("set-model", "addr:1234", nil)
			dStore.Set("set-model", expected)

			m, ok := localStore.Get("set-model")
			Expect(ok).To(BeTrue())
			Expect(m).To(Equal(expected))
		})
	})

	Context("Delete", func() {
		It("removes from local store", func() {
			localStore.Set("del-model", model.NewModel("del-model", "addr", nil))
			dStore.Delete("del-model")

			_, ok := localStore.Get("del-model")
			Expect(ok).To(BeFalse())
		})
	})

	Context("Range", func() {
		It("returns local-only models", func() {
			localStore.Set("local-a", model.NewModel("local-a", "addr-a", nil))
			localStore.Set("local-b", model.NewModel("local-b", "addr-b", nil))

			visited := map[string]bool{}
			dStore.Range(func(id string, m *model.Model) bool {
				visited[id] = true
				return true
			})
			Expect(visited).To(HaveLen(2))
			Expect(visited).To(HaveKey("local-a"))
			Expect(visited).To(HaveKey("local-b"))
		})

		It("returns DB-only models not in local cache", func() {
			node := &nodes.BackendNode{
				Name: "range-node", Address: "range:9000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "db-only-model", "loaded")).To(Succeed())

			visited := map[string]bool{}
			dStore.Range(func(id string, m *model.Model) bool {
				visited[id] = true
				return true
			})
			Expect(visited).To(HaveKey("db-only-model"))
		})

		It("deduplicates models present in both local and DB", func() {
			node := &nodes.BackendNode{
				Name: "dup-node", Address: "dup:9000",
			}
			Expect(registry.Register(context.Background(), node, true)).To(Succeed())
			Expect(registry.SetNodeModel(context.Background(), node.ID, "shared-model", "loaded")).To(Succeed())

			// Also in local store
			localStore.Set("shared-model", model.NewModel("shared-model", "dup:9000", nil))

			count := 0
			dStore.Range(func(id string, m *model.Model) bool {
				if id == "shared-model" {
					count++
				}
				return true
			})
			Expect(count).To(Equal(1))
		})

		It("stops early when callback returns false", func() {
			localStore.Set("r1", model.NewModel("r1", "a", nil))
			localStore.Set("r2", model.NewModel("r2", "b", nil))
			localStore.Set("r3", model.NewModel("r3", "c", nil))

			count := 0
			dStore.Range(func(id string, m *model.Model) bool {
				count++
				return false
			})
			Expect(count).To(Equal(1))
		})
	})
})
