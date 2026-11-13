package gormstore_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// BenchmarkCommitUpper commits n new files of about 4 KiB each as one agent
// turn, to a SQLite store and a filesystem blob store in a fresh folder. The
// cost of the commit is mostly the syncs of the blob store, so run it on the
// disk of the target host:
//
//	go test ./core/services/studio/workspace/gormstore -run '^$' -bench CommitUpper -benchtime 1x
func BenchmarkCommitUpper(b *testing.B) {
	for _, n := range []int{1, 101, 1001, 5000} {
		b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				s, v := commitFixture(b)
				src := workspace.FixtureSource{workspace.FixtureDir("w"), workspace.FixtureDir("w/many")}
				for i := range n {
					body := fmt.Sprintf("%d\n", i) + strings.Repeat("x", 4090)
					src = append(src, workspace.FixtureFile(fmt.Sprintf("w/many/f%d.txt", i), body))
				}
				b.StartTimer()
				if _, _, err := s.CommitUpper(context.Background(), v, wstest.AliceAdmin(), 0, src, workspace.UpperOptions{Title: "many"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func commitFixture(b *testing.B) (workspace.Store, workspace.View) {
	dir := b.TempDir()
	db, err := sqlitedb.Open("file:"+filepath.Join(dir, "ws.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	if err != nil {
		b.Fatal(err)
	}
	s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Mkdir(ctx, wstest.AdminActor, "/w"); err != nil {
		b.Fatal(err)
	}
	v, err := s.View(ctx, wstest.AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeWrite}})
	if err != nil {
		b.Fatal(err)
	}
	return s, v
}
