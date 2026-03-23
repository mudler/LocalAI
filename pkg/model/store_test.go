package model_test

import (
	"os"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("InMemoryModelStore", func() {
	var store *model.InMemoryModelStore

	BeforeEach(func() {
		store = model.NewInMemoryModelStore()
	})

	It("Get returns false for missing key", func() {
		m, ok := store.Get("nonexistent")
		Expect(ok).To(BeFalse())
		Expect(m).To(BeNil())
	})

	It("Set then Get returns the model", func() {
		expected := model.NewModel("test-model", "localhost:5000", nil)
		store.Set("test-model", expected)

		m, ok := store.Get("test-model")
		Expect(ok).To(BeTrue())
		Expect(m).To(Equal(expected))
	})

	It("Set overwrites existing entry", func() {
		first := model.NewModel("m1", "addr1", nil)
		second := model.NewModel("m1", "addr2", nil)

		store.Set("m1", first)
		store.Set("m1", second)

		m, ok := store.Get("m1")
		Expect(ok).To(BeTrue())
		Expect(m).To(Equal(second))
	})

	It("Delete removes model, Get returns false", func() {
		store.Set("to-delete", model.NewModel("to-delete", "addr", nil))
		store.Delete("to-delete")

		m, ok := store.Get("to-delete")
		Expect(ok).To(BeFalse())
		Expect(m).To(BeNil())
	})

	It("Delete on missing key does not panic", func() {
		Expect(func() { store.Delete("no-such-key") }).ToNot(Panic())
	})

	It("Range visits all entries", func() {
		store.Set("a", model.NewModel("a", "addr-a", nil))
		store.Set("b", model.NewModel("b", "addr-b", nil))
		store.Set("c", model.NewModel("c", "addr-c", nil))

		visited := map[string]bool{}
		store.Range(func(id string, m *model.Model) bool {
			visited[id] = true
			return true
		})
		Expect(visited).To(HaveLen(3))
		Expect(visited).To(HaveKey("a"))
		Expect(visited).To(HaveKey("b"))
		Expect(visited).To(HaveKey("c"))
	})

	It("Range stops early when callback returns false", func() {
		store.Set("x", model.NewModel("x", "addr-x", nil))
		store.Set("y", model.NewModel("y", "addr-y", nil))
		store.Set("z", model.NewModel("z", "addr-z", nil))

		count := 0
		store.Range(func(id string, m *model.Model) bool {
			count++
			return false // stop after first
		})
		Expect(count).To(Equal(1))
	})

	It("Range on empty store is a no-op", func() {
		called := false
		store.Range(func(id string, m *model.Model) bool {
			called = true
			return true
		})
		Expect(called).To(BeFalse())
	})
})

var _ = Describe("ModelLoader with custom ModelStore", func() {
	var (
		modelLoader *model.ModelLoader
		modelPath   string
		customStore *model.InMemoryModelStore
	)

	BeforeEach(func() {
		modelPath = "/tmp/test_model_store_path"
		os.Mkdir(modelPath, 0755)

		systemState, err := system.GetSystemState(
			system.WithModelPath(modelPath),
		)
		Expect(err).ToNot(HaveOccurred())
		modelLoader = model.NewModelLoader(systemState)

		customStore = model.NewInMemoryModelStore()
	})

	AfterEach(func() {
		os.RemoveAll(modelPath)
	})

	Context("SetModelStore", func() {
		It("ListLoadedModels uses the custom store", func() {
			// Pre-populate the custom store
			customStore.Set("remote-model-1", model.NewModel("remote-model-1", "node1:5000", nil))
			customStore.Set("remote-model-2", model.NewModel("remote-model-2", "node2:5000", nil))

			modelLoader.SetModelStore(customStore)

			listed := modelLoader.ListLoadedModels()
			Expect(listed).To(HaveLen(2))

			ids := map[string]bool{}
			for _, m := range listed {
				ids[m.ID] = true
			}
			Expect(ids).To(HaveKey("remote-model-1"))
			Expect(ids).To(HaveKey("remote-model-2"))
		})

		It("ShutdownModel uses the custom store", func() {
			customStore.Set("shutdown-me", model.NewModel("shutdown-me", "addr", nil))
			modelLoader.SetModelStore(customStore)

			err := modelLoader.ShutdownModel("shutdown-me")
			Expect(err).ToNot(HaveOccurred())

			// Model should be removed from the custom store
			_, ok := customStore.Get("shutdown-me")
			Expect(ok).To(BeFalse())

			// ListLoadedModels should no longer include it
			Expect(modelLoader.ListLoadedModels()).To(BeEmpty())
		})
	})
})
