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

var _ = Describe("add-on pin store", func() {
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

	It("starts the local owner without implicit pins", func() {
		s := store(appOver(nil, GinkgoT().TempDir()))
		Expect(pinIDs(s, auth.AddonLocalOwnerID)).To(BeEmpty())
	})
	It("starts existing accounts without implicit pins", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Create(&auth.User{ID: "existing", Email: "existing@example.test", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		Expect(pinIDs(store(appOver(db, "")), "existing")).To(BeEmpty())
	})

	It("preserves saved user pins across a second frontend and reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "auth.db")
		db, err := auth.InitDB(path)
		Expect(err).NotTo(HaveOccurred())
		saved, err := pins.NewGormStore(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(saved.Replace(context.Background(), "u1", []string{"studio", "talk", "arbitrary"})).To(Succeed())
		a := appOver(db, "")
		s := store(a)
		Expect(store(a)).To(BeIdenticalTo(s))
		Expect(pinIDs(s, "u1")).To(Equal([]string{"studio", "talk", "arbitrary"}))
		Expect(s.Replace(context.Background(), "u1", []string{"talk"})).To(Succeed())
		Expect(pinIDs(store(appOver(db, "")), "u1")).To(Equal([]string{"talk"}))
		db2, err := auth.InitDB(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(pinIDs(store(appOver(db2, "")), "u1")).To(Equal([]string{"talk"}))
		Expect(pinIDs(store(appOver(db2, "")), "new")).To(BeEmpty())
	})

	It("opens the pin store without scanning accounts or creating seed metadata", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Migrator().RenameTable(&auth.User{}, "users_away")).To(Succeed())
		s := store(appOver(db, ""))
		Expect(pinIDs(s, "u1")).To(BeEmpty())
		Expect(db.Migrator().HasTable("addon_pin_seed_runs")).To(BeFalse())
		Expect(s.Replace(context.Background(), "u1", []string{"arbitrary"})).To(Succeed())
		Expect(pinIDs(store(appOver(db, "")), "u1")).To(Equal([]string{"arbitrary"}))
	})

	It("keeps explicit local-owner pins and unpins across reopen", func() {
		dir := GinkgoT().TempDir()
		s := store(appOver(nil, dir))
		Expect(pinIDs(s, auth.AddonLocalOwnerID)).To(BeEmpty())
		Expect(s.Replace(context.Background(), auth.AddonLocalOwnerID, []string{"studio", "talk"})).To(Succeed())
		Expect(s.Replace(context.Background(), auth.AddonLocalOwnerID, []string{"talk"})).To(Succeed())

		again := store(appOver(nil, dir))
		Expect(pinIDs(again, auth.AddonLocalOwnerID)).To(Equal([]string{"talk"}))
		raw, err := os.ReadFile(filepath.Join(dir, "addon-pins.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("seedRuns"))
		Expect(again.Replace(context.Background(), auth.AddonLocalOwnerID, []string{})).To(Succeed())
		Expect(pinIDs(store(appOver(nil, dir)), auth.AddonLocalOwnerID)).To(BeEmpty())
	})

	It("reports a corrupt pins file instead of falling back to memory", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "addon-pins.json"), []byte("{broken"), 0o600)).To(Succeed())
		a := appOver(nil, dir)
		_, err := a.AddonPinStore()
		Expect(err).To(HaveOccurred())
		Expect(os.Remove(filepath.Join(dir, "addon-pins.json"))).To(Succeed())
		s, again := a.AddonPinStore()
		Expect(s).To(BeNil())
		Expect(again).To(BeIdenticalTo(err))
	})
})
