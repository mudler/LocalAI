package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// fakeAliasResolver maps alias names to targets from a plain map, standing in
// for the config loader so these specs don't need a model directory.
type fakeAliasResolver struct{ aliases map[string]string }

func (f *fakeAliasResolver) ResolveAliasName(name string) (string, bool) {
	target, ok := f.aliases[name]
	if !ok {
		return name, false
	}
	return target, true
}

var _ = Describe("Alias-keyed scheduling rules", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		resolver *fakeAliasResolver
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		resolver = &fakeAliasResolver{aliases: map[string]string{"production": "qwen3"}}
		registry.SetAliasResolver(resolver)
	})

	set := func(cfg *ModelSchedulingConfig) {
		ExpectWithOffset(1, registry.SetModelScheduling(context.Background(), cfg)).To(Succeed())
	}

	Describe("resolving a rule to the model it governs", func() {
		It("reports the alias target as a rule's target model", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})

			got, err := registry.GetModelScheduling(context.Background(), "production")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).ToNot(BeNil())
			// The rule keeps the operator's name; only what it governs resolves.
			Expect(got.ModelName).To(Equal("production"))
			Expect(got.Target()).To(Equal("qwen3"))
		})

		It("reports a plain model rule as governing itself", func() {
			set(&ModelSchedulingConfig{ModelName: "qwen3", MinReplicas: 1})

			got, err := registry.GetModelScheduling(context.Background(), "qwen3")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Target()).To(Equal("qwen3"))
		})

		It("resolves targets when listing every rule", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})

			configs, err := registry.ListModelSchedulings(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Target()).To(Equal("qwen3"))
		})

		It("resolves targets when listing auto-scaling rules", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})

			configs, err := registry.ListAutoScalingConfigs(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Target()).To(Equal("qwen3"))
		})

		It("governs the new target after the alias is repointed", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})
			resolver.aliases["production"] = "llama4"

			got, err := registry.GetModelScheduling(context.Background(), "production")
			Expect(err).ToNot(HaveOccurred())
			// The rule did not move: its settings now apply to llama4.
			Expect(got.ModelName).To(Equal("production"))
			Expect(got.Target()).To(Equal("llama4"))
			Expect(got.MinReplicas).To(Equal(2))
		})

		It("governs its own name when no resolver is installed", func() {
			plain, err := NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())
			Expect(plain.SetModelScheduling(context.Background(), &ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})).To(Succeed())

			got, err := plain.GetModelScheduling(context.Background(), "production")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Target()).To(Equal("production"))
		})
	})

	Describe("finding the rule that governs a loaded model", func() {
		It("finds an alias rule from the model the alias resolves to", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2, NodeSelector: `{"tier":"gpu"}`})

			got, err := registry.GetGoverningScheduling(context.Background(), "qwen3")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).ToNot(BeNil())
			Expect(got.ModelName).To(Equal("production"))
			Expect(got.NodeSelector).To(Equal(`{"tier":"gpu"}`))
		})

		It("returns nil when nothing governs the model", func() {
			got, err := registry.GetGoverningScheduling(context.Background(), "qwen3")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeNil())
		})

		It("prefers a rule on the model itself over an alias rule", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})
			set(&ModelSchedulingConfig{ModelName: "qwen3", MinReplicas: 7})

			got, err := registry.GetGoverningScheduling(context.Background(), "qwen3")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.ModelName).To(Equal("qwen3"))
			Expect(got.MinReplicas).To(Equal(7))
		})

		// Two aliases onto one model is a conflict the write paths reject, but
		// a config-file edit can still produce it. Whichever rule wins, it must
		// be the same one on every frontend and every tick.
		It("breaks a two-alias tie deterministically on the older rule", func() {
			resolver.aliases["staging"] = "qwen3"
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})
			set(&ModelSchedulingConfig{ModelName: "staging", MinReplicas: 5})

			got, err := registry.GetGoverningScheduling(context.Background(), "qwen3")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.ModelName).To(Equal("production"))
		})

		It("stops governing the old target once the alias is repointed", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})
			resolver.aliases["production"] = "llama4"

			gone, err := registry.GetGoverningScheduling(context.Background(), "qwen3")
			Expect(err).ToNot(HaveOccurred())
			Expect(gone).To(BeNil())

			moved, err := registry.GetGoverningScheduling(context.Background(), "llama4")
			Expect(err).ToNot(HaveOccurred())
			Expect(moved).ToNot(BeNil())
			Expect(moved.ModelName).To(Equal("production"))
		})

		It("ignores an alias rule whose target is a different model", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})

			got, err := registry.GetGoverningScheduling(context.Background(), "some-other-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeNil())
		})
	})

	Describe("rules that already resolve to a model", func() {
		It("reports the rule already governing a target", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})

			conflict, err := registry.SchedulingConflict(context.Background(), "qwen3", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(conflict).To(Equal("production"))
		})

		It("does not report the rule being edited as its own conflict", func() {
			set(&ModelSchedulingConfig{ModelName: "production", MinReplicas: 2})

			conflict, err := registry.SchedulingConflict(context.Background(), "qwen3", "production")
			Expect(err).ToNot(HaveOccurred())
			Expect(conflict).To(BeEmpty())
		})

		It("reports no conflict when the target is free", func() {
			conflict, err := registry.SchedulingConflict(context.Background(), "qwen3", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(conflict).To(BeEmpty())
		})
	})
})
