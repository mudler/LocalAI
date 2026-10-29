package gormstore_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

var schemaSeq atomic.Int64

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
		// Each store gets its own schema: a spec that opens two stores (the
		// "same calls give the same tree" property) needs two independent
		// trees, and one shared set of tables made the second wipe the first.
		wstest.RunStoreSuite(func() workspace.Store {
			schema := fmt.Sprintf("ws_%d_%d", os.Getpid(), schemaSeq.Add(1))
			admin, err := gorm.Open(postgres.Open(url), &gorm.Config{})
			Expect(err).ToNot(HaveOccurred())
			adminRaw, _ := admin.DB()
			Expect(admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`).Error).To(Succeed())
			Expect(admin.Exec(`CREATE SCHEMA ` + schema).Error).To(Succeed())
			DeferCleanup(func() {
				_ = admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`).Error
				_ = adminRaw.Close()
			})
			db, err := gorm.Open(postgres.Open(url+"&search_path="+schema), &gorm.Config{})
			Expect(err).ToNot(HaveOccurred())
			raw, _ := db.DB()
			DeferCleanup(func() { _ = raw.Close() })
			blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
			Expect(err).ToNot(HaveOccurred())
			s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin})
			Expect(err).ToNot(HaveOccurred())
			return s
		})
	})
})
