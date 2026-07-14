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

var _ = Describe("ModelConfigLoader alias resolution", func() {
	var loader *ModelConfigLoader

	BeforeEach(func() {
		loader = NewModelConfigLoader("")
		loader.configs["real"] = ModelConfig{Name: "real", Backend: "llama-cpp"}
		loader.configs["gpt-4"] = ModelConfig{Name: "gpt-4", Alias: "real"}
		loader.configs["chain"] = ModelConfig{Name: "chain", Alias: "gpt-4"}
		loader.configs["dangling"] = ModelConfig{Name: "dangling", Alias: "nope"}
	})

	It("returns non-alias configs unchanged", func() {
		cfg := loader.configs["real"]
		got, was, err := loader.ResolveAlias(&cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(was).To(BeFalse())
		Expect(got.Name).To(Equal("real"))
	})

	It("resolves an alias to its target", func() {
		cfg := loader.configs["gpt-4"]
		got, was, err := loader.ResolveAlias(&cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(was).To(BeTrue())
		Expect(got.Name).To(Equal("real"))
	})

	It("rejects an alias chain", func() {
		cfg := loader.configs["chain"]
		_, was, err := loader.ResolveAlias(&cfg)
		Expect(was).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("chains are not allowed")))
	})

	It("rejects a dangling alias", func() {
		cfg := loader.configs["dangling"]
		_, _, err := loader.ResolveAlias(&cfg)
		Expect(err).To(MatchError(ContainSubstring("unknown model")))
	})

	It("ValidateAliasTarget passes for a real target and fails for a chain", func() {
		good := loader.configs["gpt-4"]
		Expect(loader.ValidateAliasTarget(&good)).ToNot(HaveOccurred())
		bad := loader.configs["chain"]
		Expect(loader.ValidateAliasTarget(&bad)).To(MatchError(ContainSubstring("itself an alias")))
	})
})
