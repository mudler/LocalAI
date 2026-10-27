package triggers_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/triggers/triggerstest"
)

func TestJSONInMemory(t *testing.T) {
	triggerstest.RunStoreSuite(t, func(t *testing.T) triggers.Store {
		s, err := triggers.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	triggerstest.RunStoreSuite(t, func(t *testing.T) triggers.Store {
		s, err := triggers.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func openSQLiteFile(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestGORMOnSQLite(t *testing.T) {
	triggerstest.RunStoreSuite(t, func(t *testing.T) triggers.Store {
		s, err := triggers.NewGORM(openSQLiteFile(t, filepath.Join(t.TempDir(), "t.db")))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
func TestGORMOnPostgres(t *testing.T) {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("LOCALAI_TEST_POSTGRES_URL is not set")
	}
	triggerstest.RunStoreSuite(t, func(t *testing.T) triggers.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		s, err := triggers.NewGORM(db)
		if err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"studio_trigger_state", "studio_trigger_runs", "studio_triggers"} {
			if err := db.Exec("DELETE FROM " + tbl).Error; err != nil {
				t.Fatal(err)
			}
		}
		return s
	})
}

func sampleTrigger(id string) triggers.Trigger {
	return triggers.Trigger{ID: id, Owner: "u1", Name: "n", Prompt: "p", Enabled: true, Kind: triggers.KindSchedule, Schedule: "daily 08:00",
		TZ: "UTC", Since: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), AgentID: "a1", SourceChatID: "c1", Model: "m"}
}

func TestJSONSurvivesReopen(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	first, _ := triggers.NewJSON(dir)
	if err := first.PutTrigger(ctx, sampleTrigger("t1")); err != nil {
		t.Fatal(err)
	}
	ok, err := first.Claim(ctx, triggers.Run{ID: "r1", TriggerID: "t1", DueKey: "k1", Status: triggers.StatusRunning, ChatID: "chat9"})
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	_ = first.SetCursor(ctx, 42)
	_ = first.SetLastDue(ctx, "t1", time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC))

	second, err := triggers.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := second.GetTrigger(ctx, "t1")
	if err != nil || got.AgentID != "a1" || got.SourceChatID != "c1" || got.Model != "m" {
		t.Fatalf("trigger after reopen: %+v %v", got, err)
	}
	if again, _ := second.Claim(ctx, triggers.Run{ID: "r2", TriggerID: "t1", DueKey: "k1"}); again {
		t.Fatal("a due key ran twice across a restart")
	}
	if r, err := second.RunByChat(ctx, "chat9"); err != nil || r.ID != "r1" {
		t.Fatalf("run by chat: %+v %v", r, err)
	}
	if rev, ok, _ := second.Cursor(ctx); !ok || rev != 42 {
		t.Fatalf("cursor = %d %v", rev, ok)
	}
	if d, _ := second.LastDue(ctx, "t1"); d.IsZero() {
		t.Fatal("last due lost")
	}
}

// Two stores on one database are two frontends: a due key is claimed once.
func TestTwoGORMStoresClaimADueKeyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	a, err := triggers.NewGORM(openSQLiteFile(t, path))
	if err != nil {
		t.Fatal(err)
	}
	b, err := triggers.NewGORM(openSQLiteFile(t, path))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const rounds = 20
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		wins := make(chan string, 8)
		for i, s := range []triggers.Store{a, b, a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := fmt.Sprintf("r%d-%d", round, i)
				if ok, err := s.Claim(ctx, triggers.Run{ID: id, TriggerID: "t1", DueKey: fmt.Sprintf("k%d", round), Status: triggers.StatusQueued}); err != nil {
					t.Errorf("claim: %v", err)
				} else if ok {
					wins <- id
				}
			}()
		}
		wg.Wait()
		close(wins)
		n := 0
		for range wins {
			n++
		}
		if n != 1 {
			t.Fatalf("round %d: %d claims won, want 1", round, n)
		}
	}
}
