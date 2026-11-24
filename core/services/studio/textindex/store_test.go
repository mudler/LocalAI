package textindex_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
	"github.com/mudler/LocalAI/core/services/studio/textindex/textindextest"
)

func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("The JSON store", func() {
	Describe("in a folder", func() {
		textindextest.Run(func() textindex.Store {
			s, err := textindex.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	Describe("in memory", func() {
		textindextest.Run(func() textindex.Store {
			s, err := textindex.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})
	It("keeps its entries across a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := textindex.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		textindextest.Seed(first)
		second, err := textindex.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		textindextest.CheckSeed(second)
	})
	It("keeps the texts out of the user's folders", func() {
		dir := GinkgoT().TempDir()
		s, err := textindex.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		textindextest.Seed(s)
		Expect(filepath.Join(dir, "texts", "1.txt")).To(BeAnExistingFile())
		info, err := os.Stat(filepath.Join(dir, "texts", "1.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})
})

var _ = Describe("The GORM store on SQLite", func() {
	textindextest.Run(func() textindex.Store {
		s, err := textindex.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "index.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})
	It("uses FTS5 with the trigram tokenizer", func() {
		s, err := textindex.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "index.db")))
		Expect(err).NotTo(HaveOccurred())
		Expect(textindex.Engine(s)).To(Equal("fts5"))
	})
	It("keeps its entries across a reopen, and indexes rows written before FTS5", func() {
		path := filepath.Join(GinkgoT().TempDir(), "index.db")
		first, err := textindex.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		textindextest.Seed(first)
		db := openSQLite(path)
		Expect(db.Exec("DROP TABLE studio_file_texts_fts").Error).To(Succeed())
		second, err := textindex.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		textindextest.CheckSeed(second)
	})
})

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("The GORM store on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("meets the contract", func() { Skip("LOCALAI_TEST_POSTGRES_URL is not set") })
		return
	}
	openPG := func() *gorm.DB {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		raw, _ := db.DB()
		DeferCleanup(func() { _ = raw.Close() })
		return db
	}
	textindextest.Run(func() textindex.Store {
		db := openPG()
		s, err := textindex.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Exec("DELETE FROM studio_file_texts").Error).To(Succeed())
		Expect(db.Exec("DELETE FROM studio_file_text_cursor").Error).To(Succeed())
		return s
	})
	It("names its search engine", func() {
		s, err := textindex.NewGORM(openPG())
		Expect(err).NotTo(HaveOccurred())
		Expect(textindex.Engine(s)).To(BeElementOf("pg_trgm", "scan"))
	})
})
