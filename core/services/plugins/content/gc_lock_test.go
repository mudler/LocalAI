//go:build auth

package content_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

var _ = Describe("GC with a database", func() {
	It("runs under the refresh lock and skips while another frontend holds it", func() {
		ctx := context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "gc.db")+"?_busy_timeout=5000"), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		c := &clock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
		ws := newWorkspace(c)
		st, err := store.NewFileStore("")
		Expect(err).NotTo(HaveOccurred())

		_, err = advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyPluginsRefresh, func() error {
			ran, err := content.GC(ctx, db, ws, root, st, c.Now())
			Expect(err).NotTo(HaveOccurred())
			Expect(ran).To(BeFalse(), "GC ran while the refresh lock was held")
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		ran, err := content.GC(ctx, db, ws, root, st, c.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(ran).To(BeTrue())
	})
})
