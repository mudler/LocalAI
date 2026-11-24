package gormstore_test

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

func TestGormstore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Workspace Gormstore Suite")
}

// sqliteStore opens a store on a SQLite file the way LocalAI does: the pure-Go
// driver, a busy timeout and immediate write transactions.
func sqliteStore(o gormstore.Options) workspace.Store {
	GinkgoHelper()
	db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "ws.db"))
	Expect(err).ToNot(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).ToNot(HaveOccurred())
	o.Admin = wstest.Admin
	s, err := gormstore.New(db, blobs, o)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return s
}

var _ = Describe("The SQLite store", func() {
	Describe("meets the contract", func() {
		wstest.RunStoreSuite(func() workspace.Store { return sqliteStore(gormstore.Options{}) })
	})
})
