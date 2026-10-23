// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("add-on pin store and the upgrade seed", func() {
	pinIDs := func(s pins.Store, user string) []string {
		list, err := s.List(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		ids := []string{}
		for _, p := range list {
			ids = append(ids, p.AddonID)
		}
		return ids
	}
	appOver := func(db *gorm.DB, dataPath string) *Application {
		return &Application{
			applicationConfig: &config.ApplicationConfig{Context: context.Background(), DataPath: dataPath},
			authDB:            db,
		}
	}
	store := func(a *Application) pins.Store {
		s, err := a.AddonPinStore()
		Expect(err).NotTo(HaveOccurred())
		return s
	}

	It("pins studio and talk once for the accounts that exist and never again", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		for _, id := range []string{"u1", "u2"} {
			Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		}
		a := appOver(db, "")
		s := store(a)
		Expect(pinIDs(s, "u1")).To(Equal([]string{"studio", "talk"}))
		Expect(pinIDs(s, "u2")).To(Equal([]string{"studio", "talk"}))

		// An account created later starts with no pins, and a second seed does nothing.
		Expect(db.Create(&auth.User{ID: "u3", Email: "u3@example.test", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		Expect(pinIDs(s, "u3")).To(BeEmpty())
		ran, err := s.SeedExisting(context.Background(), []string{"u3"}, pins.SeedIDs)
		Expect(err).NotTo(HaveOccurred())
		Expect(ran).To(BeFalse())

		// The store is created once per application.
		Expect(store(a)).To(BeIdenticalTo(s))

		// An unpin sticks across a restart or a second frontend over the same database.
		Expect(s.Replace(context.Background(), "u1", []string{"talk"})).To(Succeed())
		again := store(appOver(db, ""))
		Expect(pinIDs(again, "u1")).To(Equal([]string{"talk"}))
		Expect(pinIDs(again, "u3")).To(BeEmpty())
	})

	It("does not mark the seed as run when the accounts cannot be read, and seeds at the next start", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Create(&auth.User{ID: "u1", Email: "u1@example.test", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		Expect(db.Migrator().RenameTable(&auth.User{}, "users_away")).To(Succeed())

		s := store(appOver(db, ""))
		Expect(pinIDs(s, "u1")).To(BeEmpty())
		var marks int64
		Expect(db.Model(&pins.SeedRun{}).Count(&marks).Error).To(Succeed())
		Expect(marks).To(BeZero())

		Expect(db.Migrator().RenameTable("users_away", &auth.User{})).To(Succeed())
		next := store(appOver(db, ""))
		Expect(pinIDs(next, "u1")).To(Equal([]string{"studio", "talk"}))
	})

	It("seeds the local owner when authentication is off and keeps an unpin", func() {
		dir := GinkgoT().TempDir()
		s := store(appOver(nil, dir))
		Expect(pinIDs(s, auth.AddonLocalOwnerID)).To(Equal([]string{"studio", "talk"}))
		Expect(s.Replace(context.Background(), auth.AddonLocalOwnerID, []string{"talk"})).To(Succeed())

		again := store(appOver(nil, dir))
		Expect(pinIDs(again, auth.AddonLocalOwnerID)).To(Equal([]string{"talk"}))
		raw, err := os.ReadFile(filepath.Join(dir, "addon-pins.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(pins.UpgradeSeedName))
	})

	It("reports a corrupt pins file instead of falling back to memory", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "addon-pins.json"), []byte("{broken"), 0o600)).To(Succeed())
		_, err := appOver(nil, dir).AddonPinStore()
		Expect(err).To(HaveOccurred())
	})
})
