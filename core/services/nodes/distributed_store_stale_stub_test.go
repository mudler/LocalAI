package nodes

import (
	"context"
	"fmt"
	"path/filepath"

	process "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/pkg/model"
)

func listedModels(s *DistributedModelStore) []string {
	listed := []string{}
	s.Range(func(id string, _ *model.Model) bool {
		listed = append(listed, id)
		return true
	})
	return listed
}

// A frontend keeps a stub for each model it routed. The stub is dropped by a
// replica-removed hook, and the hook runs only in the process that deleted the
// registry rows. With several frontends the drain lands on one of them, and
// the others keep their stubs. The registry is the source of truth, so Range
// must not list a stub that the registry no longer holds.
var _ = Describe("DistributedModelStore with stale stubs", func() {
	var (
		ctx            context.Context
		registryA      *NodeRegistry
		registryB      *NodeRegistry
		localA, localB *model.InMemoryModelStore
		frontA, frontB *DistributedModelStore
		worker         *BackendNode
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "nodes.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).ToNot(HaveOccurred())
		sqlDB.SetMaxOpenConns(1)

		// Two frontends, one shared registry database.
		registryA, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		registryB, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		localA = model.NewInMemoryModelStore()
		localB = model.NewInMemoryModelStore()
		frontA = NewDistributedModelStore(localA, registryA)
		frontB = NewDistributedModelStore(localB, registryB)
		registryA.AddReplicaRemovedHook(NewLocalStubInvalidator(registryA, frontA))
		registryB.AddReplicaRemovedHook(NewLocalStubInvalidator(registryB, frontB))

		worker = &BackendNode{Name: "worker-1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
		Expect(registryA.Register(ctx, worker, true)).To(Succeed())
	})

	routeOnA := func(name string) {
		Expect(registryA.SetNodeModel(ctx, worker.ID, name, 0, "loaded", "10.0.0.1:12345", 0)).To(Succeed())
		localA.Set(name, model.NewModel(name, "10.0.0.1:12345", nil))
	}

	It("lists a routed model while the registry holds it", func() {
		routeOnA("m")
		Expect(listedModels(frontA)).To(ConsistOf("m"))
		Expect(listedModels(frontB)).To(ConsistOf("m"))
	})

	It("does not list a model that another frontend drained", func() {
		routeOnA("m")

		// The drain request lands on frontend B: B deletes the rows and runs
		// only its own hooks.
		Expect(registryB.MarkDraining(ctx, worker.ID)).To(Succeed())

		Expect(listedModels(frontB)).ToNot(ContainElement("m"))
		Expect(listedModels(frontA)).ToNot(ContainElement("m"), "frontend A must not list a model that no node holds")

		_, ok := localA.Get("m")
		Expect(ok).To(BeFalse(), "the stale stub is dropped, so Get and CheckIsLoaded heal too")
	})

	It("does not list a model that another frontend deregistered", func() {
		routeOnA("m")
		Expect(registryB.Deregister(ctx, worker.ID)).To(Succeed())
		Expect(listedModels(frontA)).ToNot(ContainElement("m"))
	})

	It("hides the model of an unhealthy node and shows it again when the node recovers", func() {
		routeOnA("m")

		Expect(registryA.MarkUnhealthy(ctx, worker.ID)).To(Succeed())
		Expect(listedModels(frontA)).ToNot(ContainElement("m"))
		Expect(listedModels(frontB)).ToNot(ContainElement("m"))

		// MarkUnhealthy keeps the rows, so the registry lists the model again
		// once the node is back.
		Expect(registryA.MarkHealthy(ctx, worker.ID)).To(Succeed())
		Expect(listedModels(frontA)).To(ConsistOf("m"))
	})

	It("keeps a model that has a process of this frontend", func() {
		p := process.New(process.WithTemporaryStateDir(), process.WithName("local-backend"))
		localA.Set("local-only", model.NewModel("local-only", "127.0.0.1:40000", p))

		Expect(listedModels(frontA)).To(ConsistOf("local-only"))
		_, ok := localA.Get("local-only")
		Expect(ok).To(BeTrue())
	})

	It("stops when the caller says stop", func() {
		routeOnA("m1")
		routeOnA("m2")
		calls := 0
		frontA.Range(func(string, *model.Model) bool {
			calls++
			return false
		})
		Expect(calls).To(Equal(1))
	})
})

var _ = Describe("DistributedModelStore when the registry query fails", func() {
	It("keeps listing the local stubs, as before", func() {
		local := model.NewInMemoryModelStore()
		lookup := newFakeModelLookup()
		lookup.allModelsErr = fmt.Errorf("db connection lost")
		store := NewDistributedModelStore(local, lookup)
		local.Set("stub", model.NewModel("stub", "10.0.0.1:12345", nil))

		Expect(listedModels(store)).To(ConsistOf("stub"))
		_, ok := local.Get("stub")
		Expect(ok).To(BeTrue(), "a failed query is no evidence that the model is gone")
	})
})
