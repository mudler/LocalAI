package gormstore_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("The PostgreSQL store", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("meets the contract", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}
	Describe("meets the contract", func() {
		wstest.RunStoreSuite(func() workspace.Store {
			db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
			Expect(err).ToNot(HaveOccurred())
			raw, _ := db.DB()
			DeferCleanup(func() { _ = raw.Close() })
			Expect(db.Exec(`DROP TABLE IF EXISTS studio_ws_grants, studio_ws_journal, studio_ws_changesets, studio_ws_nodes, studio_ws_meta CASCADE`).Error).To(Succeed())
			blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
			Expect(err).ToNot(HaveOccurred())
			s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin})
			Expect(err).ToNot(HaveOccurred())
			return s
		})
	})
})
