package jsonstore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

// openStore opens a store on dir, with blobs in the same folder, and closes it
// with t.Cleanup.
func openStore(t *testing.T, dir string, o jsonstore.Options) *jsonstore.Store {
	t.Helper()
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	o.Admin = wstest.Admin
	s, err := jsonstore.New(filepath.Join(dir, "index"), blobs, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestJSONStoreMeetsTheContract(t *testing.T) {
	wstest.RunStoreSuite(t, func(t *testing.T) workspace.Store {
		return openStore(t, t.TempDir(), jsonstore.Options{})
	})
}

func TestMemoryStoreMeetsTheContract(t *testing.T) {
	wstest.RunStoreSuite(t, func(t *testing.T) workspace.Store {
		blobs, err := blob.NewFS(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		s, err := jsonstore.New("", blobs, jsonstore.Options{Admin: wstest.Admin})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestAFileOverTheLimitIsRefusedAndLeavesNothing(t *testing.T) {
	s := openStore(t, t.TempDir(), jsonstore.Options{MaxFileSize: 10})
	_, err := s.Put(context.Background(), wstest.AdminActor, "/big.txt", strings.NewReader(strings.Repeat("x", 100)), workspace.PutOptions{})
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("Put over the limit = %v, want ErrTooLarge", err)
	}
	if _, err := s.Stat(context.Background(), wstest.AdminActor, "/big.txt"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("the refused file exists: %v", err)
	}
}

// A restart keeps the files, the trash and the history: the index folder and
// the blob folder are all there is.
func TestFilesSurviveReopeningTheFolder(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	first := openStore(t, dir, jsonstore.Options{})
	wstest.MustPut(t, first, wstest.AdminActor, "/Docs/kept.txt", "still here")
	if err := first.Delete(ctx, wstest.AdminActor, "/Docs/kept.txt"); err != nil {
		t.Fatal(err)
	}
	wstest.MustPut(t, first, wstest.AdminActor, "/Docs/other.txt", "me too")
	if err := first.Grant(ctx, wstest.AdminActor, "user:alice", "/Docs", workspace.ModeRead); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := openStore(t, dir, jsonstore.Options{})
	if got := wstest.MustRead(t, second, wstest.AdminActor, "/Docs/other.txt"); got != "me too" {
		t.Fatalf("after reopening, other.txt = %q", got)
	}
	if got := wstest.MustRead(t, second, wstest.Alice, "/Docs/other.txt"); got != "me too" {
		t.Fatalf("after reopening, alice reads %q: the grant was lost", got)
	}
	hist, err := second.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Path: "/Docs", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var deleteRev int64
	for _, cs := range hist {
		for _, op := range cs.Ops {
			if op.Kind == workspace.OpDelete {
				deleteRev = cs.Rev
			}
		}
	}
	if deleteRev == 0 {
		t.Fatalf("the delete is not in the history after reopening: %+v", hist)
	}
	if _, _, err := second.Undo(ctx, wstest.AdminActor, deleteRev, workspace.UndoOptions{}); err != nil {
		t.Fatalf("undo after reopening: %v", err)
	}
	if got := wstest.MustRead(t, second, wstest.AdminActor, "/Docs/kept.txt"); got != "still here" {
		t.Fatalf("after the undo, kept.txt = %q", got)
	}
}

// A crash can cut the last record of the log. The store drops that record,
// keeps a copy of the damaged log, and opens with everything before it.
func TestATornLastRecordIsDropped(t *testing.T) {
	dir := t.TempDir()
	first := openStore(t, dir, jsonstore.Options{})
	wstest.MustPut(t, first, wstest.AdminActor, "/a.txt", "one")
	wstest.MustPut(t, first, wstest.AdminActor, "/b.txt", "two")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "index", "index.log")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, raw[:len(raw)-20], 0o600); err != nil {
		t.Fatal(err)
	}

	second := openStore(t, dir, jsonstore.Options{})
	if got := wstest.MustRead(t, second, wstest.AdminActor, "/a.txt"); got != "one" {
		t.Fatalf("a.txt = %q", got)
	}
	if _, err := second.Stat(context.Background(), wstest.AdminActor, "/b.txt"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("the cut write is visible: %v", err)
	}
	damaged, _ := filepath.Glob(logPath + ".damaged-*")
	if len(damaged) != 1 {
		t.Fatalf("damaged copies = %v, want one", damaged)
	}
	// The store writes on after the cut and a third start sees all of it.
	wstest.MustPut(t, second, wstest.AdminActor, "/c.txt", "three")
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	third := openStore(t, dir, jsonstore.Options{})
	if got := wstest.MustRead(t, third, wstest.AdminActor, "/c.txt"); got != "three" {
		t.Fatalf("c.txt = %q", got)
	}
}

func TestAFolderOpenTwiceInOneProcessIsRefused(t *testing.T) {
	dir := t.TempDir()
	first := openStore(t, dir, jsonstore.Options{})
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jsonstore.New(filepath.Join(dir, "index"), blobs, jsonstore.Options{}); err == nil {
		t.Fatal("a second store on the same folder opened")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := jsonstore.New(filepath.Join(dir, "index"), blobs, jsonstore.Options{})
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	_ = again.Close()
}

// The log is folded into a snapshot when it grows. A restart reads the
// snapshot and the rest of the log, and a log that a crash left whole after
// the snapshot was written does not apply twice.
func TestTheLogIsFoldedIntoASnapshot(t *testing.T) {
	dir := t.TempDir()
	first := openStore(t, dir, jsonstore.Options{CompactAt: 2000})
	for i := range 30 {
		wstest.MustPut(t, first, wstest.AdminActor, "/f"+strings.Repeat("x", i)+".txt", "body")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, "index")
	if _, err := os.Stat(filepath.Join(index, "index.json")); err != nil {
		t.Fatalf("no snapshot after many writes: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(index, "index.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(log) > 4000 {
		t.Fatalf("the log has %d bytes, it should have been folded", len(log))
	}

	second := openStore(t, dir, jsonstore.Options{CompactAt: 2000})
	if got := len(wstest.Tree(t, second)); got != 30 {
		t.Fatalf("after reopening there are %d files, want 30", got)
	}
	hist, err := second.History(context.Background(), wstest.AdminActor, workspace.HistoryQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 30 {
		t.Fatalf("history has %d change sets, want 30", len(hist))
	}
	// A log written before the snapshot (a crash between the two steps) is skipped.
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(index, "index.log"), log, 0o600); err != nil {
		t.Fatal(err)
	}
	third := openStore(t, dir, jsonstore.Options{CompactAt: 2000})
	if got := len(wstest.Tree(t, third)); got != 30 {
		t.Fatalf("after replaying a stale log there are %d files, want 30", got)
	}
}
