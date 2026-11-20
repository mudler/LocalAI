package application

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/LocalAI/core/services/updatenotify"
)

// The notification center serves the update center and the operations with
// the chats on or off: it is built by the application, not by the chats.
var _ = Describe("the notification center without the chats", func() {
	ctx := context.Background()

	newApp := func(cfg *config.ApplicationConfig) *Application {
		cfg.DisableChats = true
		return &Application{applicationConfig: cfg}
	}

	It("starts in memory when there is no database and no data path", func() {
		app := newApp(&config.ApplicationConfig{})
		app.initNotifications()
		Expect(app.Notifier()).NotTo(BeNil())
		Expect(app.NotifyPusher()).To(BeNil(), "push keeps its subscriptions somewhere durable")
		_, err := app.Notifier().Add(ctx, notify.Notification{Principal: "u1", Title: "hello"})
		Expect(err).NotTo(HaveOccurred())
		n, err := app.Notifier().Count(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
	})

	It("keeps JSON files under the old folder of the chats when there is a data path", func() {
		dir := GinkgoT().TempDir()
		app := newApp(&config.ApplicationConfig{DataPath: dir})
		app.initNotifications()
		Expect(app.Notifier()).NotTo(BeNil())
		Expect(app.NotifyPusher()).NotTo(BeNil())
		_, err := app.Notifier().Add(ctx, notify.Notification{Principal: "u1", Title: "hello"})
		Expect(err).NotTo(HaveOccurred())
		entries, err := os.ReadDir(filepath.Join(dir, "chats", "notifications"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).NotTo(BeEmpty(), "the rows are where the chats always kept them")
		DeferCleanup(app.NotifyPusher().Close)
	})

	It("uses the table of the chats on the auth database, and a row written before is still there", func() {
		db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
		old, err := notify.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		_, err = notify.NewService(notify.Options{Store: old}).Add(ctx, notify.Notification{Principal: "u1", Title: "from the chats"})
		Expect(err).NotTo(HaveOccurred())

		app := newApp(&config.ApplicationConfig{})
		app.authDB = db
		app.initNotifications()
		Expect(app.Notifier()).NotTo(BeNil())
		list, err := app.Notifier().List(ctx, "u1", false, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Title).To(Equal("from the chats"))
		Expect(db.Migrator().HasTable("studio_notifications")).To(BeTrue())
		DeferCleanup(app.NotifyPusher().Close)
	})

	It("writes the update notices for the admin, and a keyed notice comes back as one item", func() {
		app := newApp(&config.ApplicationConfig{})
		app.initNotifications()
		em := app.updateEmitter()
		Expect(em.CheckFailed(ctx, []string{"gallery"})).To(Succeed())
		Expect(em.CheckFailed(ctx, []string{"gallery"})).To(Succeed())
		list, err := app.Notifier().List(ctx, "anonymous", false, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Code).To(Equal(updatenotify.CodeCheckFailed))
	})

	It("writes the end of a gallery operation, with the chats off, for the admin", func() {
		app := newApp(&config.ApplicationConfig{})
		app.galleryService = galleryop.NewGalleryService(app.applicationConfig, nil)
		app.initNotifications()
		app.announceOperations()
		Expect(app.galleryService.OnOpFinished).NotTo(BeNil())
		app.galleryService.OnOpFinished(galleryop.OpResult{ID: "op-1", Name: "qwen3-8b", Outcome: galleryop.OpCompleted})
		app.galleryService.OnOpFinished(galleryop.OpResult{ID: "op-1", Name: "qwen3-8b", Outcome: galleryop.OpCompleted})
		app.galleryService.OnOpFinished(galleryop.OpResult{Name: "llama-cpp", Backend: true, Outcome: galleryop.OpCompleted})
		list, err := app.Notifier().List(ctx, "anonymous", false, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1), "one item for the operation, none for the automatic pass")
		Expect(list[0].Code).To(Equal(updatenotify.CodeOpDone))
	})
})
