// SPDX-License-Identifier: MIT
//go:build auth

package chathistory_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
)

// SQLite is the default auth database. Opening it through auth.InitDB keeps
// the production DSN (busy timeout, immediate transactions).
var _ = Describe("Store on SQLite", func() {
	describeStore(func() *gorm.DB {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		return db
	})
})
