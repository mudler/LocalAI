package gormstore_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

func TestAFileOverTheLimitIsRefusedAndLeavesNothing(t *testing.T) {
	s := sqliteStore(t, gormstore.Options{MaxFileSize: 10})
	_, err := s.Put(context.Background(), wstest.AdminActor, "/big.txt", strings.NewReader(strings.Repeat("x", 100)), workspace.PutOptions{})
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("Put over the limit = %v, want ErrTooLarge", err)
	}
	if _, err := s.Stat(context.Background(), wstest.AdminActor, "/big.txt"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("the refused file exists: %v", err)
	}
}

// Two frontends share one database and one blob store. Each opens its own
// Workspace; what one writes the other reads, and their change sets never
// share a revision.
func TestTwoFrontendsShareOneDatabaseAndKeepRevisionsApart(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	open := func() workspace.Store {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a, b := open(), open()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, err := s.Put(ctx, wstest.AdminActor, fmt.Sprintf("/f%d.txt", i), strings.NewReader(fmt.Sprint(i)), workspace.PutOptions{}); err != nil {
				t.Errorf("put %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := wstest.MustRead(t, b, wstest.AdminActor, "/f0.txt"); got != "0" {
		t.Fatalf("frontend B reads %q from A's write, want 0", got)
	}
	hist, err := a.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, cs := range hist {
		if seen[cs.Rev] {
			t.Fatalf("revision %d appears twice", cs.Rev)
		}
		seen[cs.Rev] = true
	}
	if len(seen) != 20 {
		t.Fatalf("history has %d change sets, want 20", len(seen))
	}
}
