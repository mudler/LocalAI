package notify_test

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

func openSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := auth.OpenSQLiteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestGORMOnSQLite(t *testing.T) {
	notifytest.Run(t, func(t *testing.T) notify.Store {
		s, err := notify.NewGORM(openSQLite(t, filepath.Join(t.TempDir(), "studio.db")))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestGORMSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.db")
	first, err := notify.NewGORM(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	notifytest.Seed(t, first)
	second, err := notify.NewGORM(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	notifytest.CheckSeed(t, second)
}

func TestGORMPushOnSQLite(t *testing.T) {
	notifytest.RunPush(t, func(t *testing.T) notify.PushStore {
		s, err := notify.NewGORMPush(openSQLite(t, filepath.Join(t.TempDir(), "push.db")))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestGORMPushSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push.db")
	first, err := notify.NewGORMPush(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	notifytest.SeedPush(t, first)
	second, err := notify.NewGORMPush(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	notifytest.CheckPushSeed(t, second)
}
