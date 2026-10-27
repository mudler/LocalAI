//go:build auth

package auth_test

import (
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
)

// A private SQLite file for a feature that has its own tables must not get the
// auth tables.
func TestOpenSQLiteFileOpensWithoutTheAuthTables(t *testing.T) {
	db, err := auth.OpenSQLiteFile(filepath.Join(t.TempDir(), "private.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	defer raw.Close()
	if err := db.Exec(`CREATE TABLE probe (id INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable("users") {
		t.Fatal("the private database has the auth tables")
	}
}
