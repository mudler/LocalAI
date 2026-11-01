package loops_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/loops/loopstest"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func openSQLiteFile(path string) *gorm.DB {
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

var _ = Describe("Loop stores", func() {
	Describe("JSON in memory", func() {
		loopstest.RunStoreSuite(func() loops.Store {
			s, err := loops.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("JSON on disk", func() {
		loopstest.RunStoreSuite(func() loops.Store {
			s, err := loops.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})

		It("reads back what it wrote after a restart", func() {
			dir := GinkgoT().TempDir()
			s, err := loops.NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			l := loops.Loop{ID: "l1", Owner: "u1", ChatID: "c1", Prompt: "p", Interval: time.Minute, State: loops.StateActive}
			Expect(s.Add(GinkgoT().Context(), l, loops.Limits{})).To(Succeed())
			Expect(s.SetConsented(GinkgoT().Context(), "u1", "c1")).To(Succeed())
			again, err := loops.NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			got, err := again.Get(GinkgoT().Context(), "l1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Interval).To(Equal(time.Minute))
			ok, _ := again.Consented(GinkgoT().Context(), "u1", "c1")
			Expect(ok).To(BeTrue())
		})
	})

	Describe("GORM on SQLite", func() {
		loopstest.RunStoreSuite(func() loops.Store {
			s, err := loops.NewGORM(openSQLiteFile(filepath.Join(GinkgoT().TempDir(), "l.db")))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	// The PostgreSQL run needs a database that tests may wipe. Set
	// LOCALAI_TEST_POSTGRES_URL to enable it.
	Describe("GORM on PostgreSQL", func() {
		url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
		if url == "" {
			It("needs LOCALAI_TEST_POSTGRES_URL", func() {
				Skip("LOCALAI_TEST_POSTGRES_URL is not set")
			})
			return
		}
		loopstest.RunStoreSuite(func() loops.Store {
			db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			sqlDB, _ := db.DB()
			DeferCleanup(func() { _ = sqlDB.Close() })
			s, err := loops.NewGORM(db)
			Expect(err).NotTo(HaveOccurred())
			for _, tbl := range []string{"studio_chat_loops", "studio_chat_loop_owners", "studio_chat_loop_consents"} {
				Expect(db.Exec("DELETE FROM " + tbl).Error).To(Succeed())
			}
			return s
		})
	})
})
