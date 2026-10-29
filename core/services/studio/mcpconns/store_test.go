package mcpconns_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns/mcpconnstest"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func openSQLite(path string) *gorm.DB {
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

var _ = Describe("Connector stores", func() {
	Describe("JSON in memory", func() {
		mcpconnstest.Run(func() mcpconns.Store {
			s, err := mcpconns.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("JSON on disk", func() {
		mcpconnstest.Run(func() mcpconns.Store {
			s, err := mcpconns.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("JSON survives a reopen and keeps its file private", func() {
		dir := GinkgoT().TempDir()
		first, _ := mcpconns.NewJSON(dir)
		mcpconnstest.Seed(first)
		second, err := mcpconns.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		mcpconnstest.CheckSeed(second)
		st, err := os.Stat(filepath.Join(dir, "mcp-connectors.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	Describe("GORM on SQLite", func() {
		mcpconnstest.Run(func() mcpconns.Store {
			s, err := mcpconns.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "c.db")))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("GORM survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "c.db")
		first, _ := mcpconns.NewGORM(openSQLite(path))
		mcpconnstest.Seed(first)
		second, err := mcpconns.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		mcpconnstest.CheckSeed(second)
	})
})
