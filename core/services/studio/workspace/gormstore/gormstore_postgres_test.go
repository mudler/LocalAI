package gormstore_test

import (
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
func TestPostgresStoreMeetsTheContract(t *testing.T) {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("LOCALAI_TEST_POSTGRES_URL is not set")
	}
	wstest.RunStoreSuite(t, func(t *testing.T) workspace.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := db.DB()
		t.Cleanup(func() { _ = raw.Close() })
		if err := db.Exec(`DROP TABLE IF EXISTS studio_ws_grants, studio_ws_journal, studio_ws_changesets, studio_ws_nodes, studio_ws_meta CASCADE`).Error; err != nil {
			t.Fatal(err)
		}
		blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
		if err != nil {
			t.Fatal(err)
		}
		s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}
