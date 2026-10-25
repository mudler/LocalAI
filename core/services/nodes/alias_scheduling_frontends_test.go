package nodes

import (
	"context"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// revisionAliasResolver is one frontend's view of the model configs: where
// each alias points, and the config revision behind each name.
type revisionAliasResolver struct {
	fakeAliasResolver
	revisions map[string]string
}

func (f *revisionAliasResolver) ConfigRevisionOf(name string) string {
	return f.revisions[name]
}

var _ = Describe("Alias-keyed scheduling rules across frontends", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		current  *NodeRegistry // saw the alias repoint
		stale    *NodeRegistry // missed it
		storedOf func() string
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		// Two frontends share one database, each with its own copy of the
		// model configs.
		current, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		stale, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		// The rule is created while "production" still points at qwen3.
		stale.SetAliasResolver(&revisionAliasResolver{
			fakeAliasResolver: fakeAliasResolver{aliases: map[string]string{"production": "qwen3"}},
			revisions:         map[string]string{"production": "rev-old"},
		})
		Expect(stale.SetModelScheduling(ctx, &ModelSchedulingConfig{ModelName: "production", MinReplicas: 1})).To(Succeed())

		// The alias is repointed at llama4. The cluster accepts the new
		// config revision; only one frontend has reloaded it.
		current.SetAliasResolver(&revisionAliasResolver{
			fakeAliasResolver: fakeAliasResolver{aliases: map[string]string{"production": "llama4"}},
			revisions:         map[string]string{"production": "rev-new"},
		})
		_, err = current.AdvanceModelConfigRevision(ctx, "production", "rev-new")
		Expect(err).ToNot(HaveOccurred())

		storedOf = func() string {
			var row ModelSchedulingConfig
			ExpectWithOffset(1, db.Where("model_name = ?", "production").First(&row).Error).To(Succeed())
			return row.TargetModel
		}
	})

	It("does not let a stale frontend rewrite the stored target back", func() {
		// Alternate ticks between the two frontends, as the reconciler's
		// lock hands them out.
		for range 3 {
			Expect(current.RefreshSchedulingTargets(ctx)).To(Succeed())
			Expect(storedOf()).To(Equal("llama4"))
			Expect(stale.RefreshSchedulingTargets(ctx)).To(Succeed())
			Expect(storedOf()).To(Equal("llama4"))
		}
	})

	It("makes a stale frontend's reconciler act on the stored target", func() {
		Expect(current.RefreshSchedulingTargets(ctx)).To(Succeed())

		configs, err := stale.ListAutoScalingConfigs(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(configs).To(HaveLen(1))
		Expect(configs[0].Target()).To(Equal("llama4"))
	})

	It("lets the stale frontend follow once it reloads the config", func() {
		Expect(current.RefreshSchedulingTargets(ctx)).To(Succeed())
		stale.SetAliasResolver(&revisionAliasResolver{
			fakeAliasResolver: fakeAliasResolver{aliases: map[string]string{"production": "llama4"}},
			revisions:         map[string]string{"production": "rev-new"},
		})

		configs, err := stale.ListAutoScalingConfigs(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(configs[0].Target()).To(Equal("llama4"))
	})

	It("keeps the previous behaviour when no revision was accepted for the rule", func() {
		Expect(db.Where("model_name = ?", "production").Delete(&ModelConfigState{}).Error).To(Succeed())

		Expect(current.RefreshSchedulingTargets(ctx)).To(Succeed())
		Expect(storedOf()).To(Equal("llama4"))
	})
})
