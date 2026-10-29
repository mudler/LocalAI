package auth_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
)

var _ = Describe("OpenSQLiteFile", func() {
	// A private SQLite file for a feature that has its own tables must not get the
	// auth tables.
	It("opens without the auth tables", func() {
		db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "private.db"))
		Expect(err).ToNot(HaveOccurred())
		raw, _ := db.DB()
		defer raw.Close()
		Expect(db.Exec(`CREATE TABLE probe (id INTEGER)`).Error).To(Succeed())
		Expect(db.Migrator().HasTable("users")).To(BeFalse(), "the private database has the auth tables")
	})
})
