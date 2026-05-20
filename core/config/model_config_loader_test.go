package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ModelConfigLoader.GetModelsConflictingWith", func() {
	var bcl *ModelConfigLoader

	BeforeEach(func() {
		bcl = NewModelConfigLoader("/tmp/conflict-test-models")
	})

	insert := func(cfg ModelConfig) {
		bcl.Lock()
		bcl.configs[cfg.Name] = cfg
		bcl.Unlock()
	}

	It("returns nil when the named model has no groups", func() {
		insert(ModelConfig{Name: "loner"})
		Expect(bcl.GetModelsConflictingWith("loner")).To(BeNil())
	})

	It("returns nil when the named model is unknown", func() {
		Expect(bcl.GetModelsConflictingWith("ghost")).To(BeNil())
	})

	It("returns nil when no other model shares a group", func() {
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"vision"}})
		Expect(bcl.GetModelsConflictingWith("a")).To(BeNil())
	})

	It("returns models that share at least one group", func() {
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "c", ConcurrencyGroups: []string{"vision"}})
		insert(ModelConfig{Name: "d", ConcurrencyGroups: []string{"heavy", "vision"}})

		conflicts := bcl.GetModelsConflictingWith("a")
		Expect(conflicts).To(ConsistOf("b", "d"))
	})

	It("never lists the queried model itself", func() {
		insert(ModelConfig{Name: "self", ConcurrencyGroups: []string{"heavy"}})
		Expect(bcl.GetModelsConflictingWith("self")).To(BeNil())
	})

	It("ignores disabled conflicting models", func() {
		disabled := true
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"heavy"}, Disabled: &disabled})
		Expect(bcl.GetModelsConflictingWith("a")).To(BeNil())
	})

	It("normalizes groups so whitespace and duplicates do not break overlap", func() {
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{" heavy "}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"heavy", "heavy"}})
		Expect(bcl.GetModelsConflictingWith("a")).To(ConsistOf("b"))
	})
})
