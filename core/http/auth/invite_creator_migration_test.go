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

// inviteCodeBeforeNullableCreator is invite_codes as earlier releases created
// it: created_by NOT NULL with a foreign key to users.
type inviteCodeBeforeNullableCreator struct {
	ID         string  `gorm:"primaryKey;size:36"`
	Code       string  `gorm:"uniqueIndex;not null;size:64"`
	CodePrefix string  `gorm:"size:12"`
	CreatedBy  string  `gorm:"size:36;not null"`
	UsedBy     *string `gorm:"size:36"`
	UsedAt     *time.Time
	ExpiresAt  time.Time `gorm:"not null;index"`
	CreatedAt  time.Time
	Creator    auth.User  `gorm:"foreignKey:CreatedBy"`
	Consumer   *auth.User `gorm:"foreignKey:UsedBy"`
}

func (inviteCodeBeforeNullableCreator) TableName() string { return "invite_codes" }

var _ = Describe("invite_codes.created_by migration", Label("auth"), func() {
	It("lets an existing database store invites without a creator and keeps its rows", func() {
		path := filepath.Join(GinkgoT().TempDir(), "auth.db")

		old, err := auth.OpenSQLiteFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(old.AutoMigrate(&auth.User{}, &inviteCodeBeforeNullableCreator{})).To(Succeed())
		admin := auth.User{ID: "admin-before", Email: "admin@before.test", Provider: auth.ProviderLocal, Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(old.Create(&admin).Error).To(Succeed())
		Expect(old.Create(&inviteCodeBeforeNullableCreator{
			ID: "invite-before", Code: "hash-before", CodePrefix: "before01",
			CreatedBy: admin.ID, ExpiresAt: time.Now().Add(time.Hour),
		}).Error).To(Succeed())
		closeDB(old)

		db, err := auth.InitDB(path)
		Expect(err).ToNot(HaveOccurred())
		defer closeDB(db)

		var kept auth.InviteCode
		Expect(db.Preload("Creator").First(&kept, "id = ?", "invite-before").Error).To(Succeed())
		Expect(kept.Creator).ToNot(BeNil())
		Expect(kept.Creator.ID).To(Equal(admin.ID))

		Expect(db.Create(&auth.InviteCode{
			ID: "invite-after", Code: "hash-after", CodePrefix: "after001",
			ExpiresAt: time.Now().Add(time.Hour),
		}).Error).To(Succeed())

		// The foreign key must survive the migration: an unknown creator is
		// still rejected.
		ghost := "no-such-user"
		Expect(db.Create(&auth.InviteCode{
			ID: "invite-ghost", Code: "hash-ghost", CodePrefix: "ghost001",
			CreatedBy: &ghost, ExpiresAt: time.Now().Add(time.Hour),
		}).Error).To(HaveOccurred())
	})
})

func closeDB(db *gorm.DB) {
	sqlDB, err := db.DB()
	Expect(err).ToNot(HaveOccurred())
	Expect(sqlDB.Close()).To(Succeed())
}
