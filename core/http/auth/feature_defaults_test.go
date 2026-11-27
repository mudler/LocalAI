//go:build auth

package auth_test

import (
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Configurable feature defaults", func() {
	It("lists p2p_pair as a general feature that is off by default and configurable", func() {
		Expect(auth.GeneralFeatures).To(ContainElement(auth.FeatureP2PPair))
		Expect(auth.AllFeatures).To(ContainElement(auth.FeatureP2PPair))
		Expect(auth.GeneralFeatureMetas()).To(ContainElement(auth.FeatureMeta{
			Key: auth.FeatureP2PPair, Label: "Pair own devices (P2P)", DefaultValue: false, ConfigurableDefault: true,
		}))
		Expect(auth.FeatureDefaultConfigurable(auth.FeatureP2PPair)).To(BeTrue())
	})

	It("keeps every other feature not configurable", func() {
		for _, f := range auth.AllFeatures {
			if f == auth.FeatureP2PPair {
				continue
			}
			Expect(auth.FeatureDefaultConfigurable(f)).To(BeFalse(), f)
		}
	})

	Context("with a database", func() {
		var (
			currentDB *gorm.DB
			user      *auth.User
			admin     *auth.User
		)

		BeforeEach(func() {
			currentDB = testDB()
			user = createTestUser(currentDB, "pair-user@example.com", auth.RoleUser, auth.ProviderLocal)
			admin = createTestUser(currentDB, "pair-admin@example.com", auth.RoleAdmin, auth.ProviderLocal)
		})

		It("is off for a normal user while no override is stored", func() {
			Expect(auth.FeatureDefault(currentDB, auth.FeatureP2PPair)).To(BeFalse())
			Expect(auth.HasFeatureAccess(currentDB, user, auth.FeatureP2PPair)).To(BeFalse())
			Expect(auth.GetPermissionMapForUser(currentDB, user)[auth.FeatureP2PPair]).To(BeFalse())
		})

		It("is on for a normal user once an admin turns the default on", func() {
			Expect(auth.SetFeatureDefault(currentDB, auth.FeatureP2PPair, true, admin.ID)).To(Succeed())
			Expect(auth.FeatureDefault(currentDB, auth.FeatureP2PPair)).To(BeTrue())
			Expect(auth.HasFeatureAccess(currentDB, user, auth.FeatureP2PPair)).To(BeTrue())
			Expect(auth.GetPermissionMapForUser(currentDB, user)[auth.FeatureP2PPair]).To(BeTrue())

			Expect(auth.SetFeatureDefault(currentDB, auth.FeatureP2PPair, false, admin.ID)).To(Succeed())
			Expect(auth.HasFeatureAccess(currentDB, user, auth.FeatureP2PPair)).To(BeFalse())
		})

		It("records who changed the default and when", func() {
			Expect(auth.SetFeatureDefault(currentDB, auth.FeatureP2PPair, true, admin.ID)).To(Succeed())
			var row auth.FeatureDefaultOverride
			Expect(currentDB.Where("feature = ?", auth.FeatureP2PPair).First(&row).Error).To(Succeed())
			Expect(row.Enabled).To(BeTrue())
			Expect(row.UpdatedBy).To(Equal(admin.ID))
			Expect(row.UpdatedAt).NotTo(BeZero())
		})

		It("lets an explicit false in the user's permissions win over an override that is on", func() {
			Expect(auth.UpdateUserPermissions(currentDB, user.ID, auth.PermissionMap{auth.FeatureP2PPair: false})).To(Succeed())
			Expect(auth.SetFeatureDefault(currentDB, auth.FeatureP2PPair, true, admin.ID)).To(Succeed())
			Expect(auth.HasFeatureAccess(currentDB, user, auth.FeatureP2PPair)).To(BeFalse())
			Expect(auth.GetPermissionMapForUser(currentDB, user)[auth.FeatureP2PPair]).To(BeFalse())
		})

		It("lets an explicit true in the user's permissions win over the default off", func() {
			Expect(auth.UpdateUserPermissions(currentDB, user.ID, auth.PermissionMap{auth.FeatureP2PPair: true})).To(Succeed())
			Expect(auth.HasFeatureAccess(currentDB, user, auth.FeatureP2PPair)).To(BeTrue())
		})

		It("always gives the feature to an admin", func() {
			Expect(auth.HasFeatureAccess(currentDB, admin, auth.FeatureP2PPair)).To(BeTrue())
			Expect(auth.GetPermissionMapForUser(currentDB, admin)[auth.FeatureP2PPair]).To(BeTrue())
			Expect(auth.SetFeatureDefault(currentDB, auth.FeatureP2PPair, false, admin.ID)).To(Succeed())
			Expect(auth.HasFeatureAccess(currentDB, admin, auth.FeatureP2PPair)).To(BeTrue())
		})

		It("refuses an override for a feature that is not configurable", func() {
			err := auth.SetFeatureDefault(currentDB, auth.FeatureChat, false, admin.ID)
			Expect(err).To(MatchError(auth.ErrFeatureDefaultNotConfigurable))
			Expect(auth.FeatureDefault(currentDB, auth.FeatureChat)).To(BeTrue())
			var n int64
			Expect(currentDB.Model(&auth.FeatureDefaultOverride{}).Count(&n).Error).To(Succeed())
			Expect(n).To(BeZero())
		})

		It("refuses an override for an unknown feature", func() {
			Expect(auth.SetFeatureDefault(currentDB, "no_such_feature", true, admin.ID)).To(MatchError(auth.ErrUnknownFeature))
		})

		It("reports the effective default and the configurable flag in the metas", func() {
			Expect(auth.SetFeatureDefault(currentDB, auth.FeatureP2PPair, true, admin.ID)).To(Succeed())
			metas := auth.EffectiveFeatureMetas(currentDB, auth.GeneralFeatureMetas())
			Expect(metas).To(ContainElement(auth.FeatureMeta{
				Key: auth.FeatureP2PPair, Label: "Pair own devices (P2P)", DefaultValue: true, ConfigurableDefault: true,
			}))
			Expect(metas).To(ContainElement(auth.FeatureMeta{
				Key: auth.FeatureAddons, Label: "Addons", DefaultValue: false, ConfigurableDefault: false,
			}))
		})
	})

	It("reaches another frontend on the same database within the refresh interval", func() {
		// Two handles on one database file stand for two frontends: each has
		// its own cache, and the database is the only thing they share.
		path := filepath.Join(GinkgoT().TempDir(), "auth.db")
		dbA, err := auth.InitDB(path)
		Expect(err).NotTo(HaveOccurred())
		dbB, err := auth.InitDB(path)
		Expect(err).NotTo(HaveOccurred())

		now := time.Now()
		restore := auth.SetFeatureDefaultsClock(func() time.Time { return now })
		DeferCleanup(restore)

		Expect(auth.FeatureDefault(dbB, auth.FeatureP2PPair)).To(BeFalse())
		Expect(auth.SetFeatureDefault(dbA, auth.FeatureP2PPair, true, "admin")).To(Succeed())

		// The writer sees its own write at once.
		Expect(auth.FeatureDefault(dbA, auth.FeatureP2PPair)).To(BeTrue())
		// The other frontend keeps its cached value until the cache expires.
		Expect(auth.FeatureDefault(dbB, auth.FeatureP2PPair)).To(BeFalse())
		now = now.Add(auth.FeatureDefaultsRefresh + time.Second)
		Expect(auth.FeatureDefault(dbB, auth.FeatureP2PPair)).To(BeTrue())
	})
})
