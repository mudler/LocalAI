package store_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"

	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/store/storetest"
)

func TestJSONInMemory(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := store.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := store.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// A store reopened on the same directory sees what the first one wrote: this
// is what makes the JSON store a real persistence mode and not only a cache.
func TestJSONSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	storetest.Seed(t, first)
	second, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	storetest.CheckSeed(t, second)
}

func chatFile(dir, id, ext string) string {
	return filepath.Join(dir, "chats", base64.RawURLEncoding.EncodeToString([]byte(id))+ext)
}

func newChat(t *testing.T, s store.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := s.CreateChat(context.Background(), session.ChatRecord{ID: id, Principal: "alice", Title: id, Created: now, Updated: now}); err != nil {
		t.Fatal(err)
	}
}

// A crash can leave half a line at the end of a transcript. The store must
// still open, drop the half line, and append on a clean line.
func TestJSONTornTranscriptLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	newChat(t, first, "a")
	for _, txt := range []string{"one", "two"} {
		if err := first.AppendEntry(ctx, "a", session.Entry{Role: "user", Text: txt}); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.OpenFile(chatFile(dir, "a", ".entries"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"Seq":3,"Role":"us`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	second, err := store.NewJSON(dir)
	if err != nil {
		t.Fatalf("NewJSON with a torn transcript: %v", err)
	}
	es, _ := second.Entries(ctx, "a", 0, 10)
	if len(es) != 2 {
		t.Fatalf("entries = %d, want the 2 complete ones", len(es))
	}
	if err := second.AppendEntry(ctx, "a", session.Entry{Role: "user", Text: "three"}); err != nil {
		t.Fatal(err)
	}
	third, err := store.NewJSON(dir)
	if err != nil {
		t.Fatalf("reopen after append: %v", err)
	}
	es, _ = third.Entries(ctx, "a", 0, 10)
	if len(es) != 3 || es[2].Text != "three" || es[2].Seq != 3 {
		t.Fatalf("entries after append = %+v", es)
	}
}

// One damaged record must not take the other chats with it.
func TestJSONCorruptRecordSkipped(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	newChat(t, first, "good")
	newChat(t, first, "bad")
	if err := os.WriteFile(chatFile(dir, "bad", ".json"), []byte(`{"rec":{"ID":"ba`), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := store.NewJSON(dir)
	if err != nil {
		t.Fatalf("NewJSON with a corrupt record: %v", err)
	}
	if _, err := second.GetChat(ctx, "alice", "good"); err != nil {
		t.Fatalf("good chat: %v", err)
	}
	if err := second.AppendEntry(ctx, "good", session.Entry{Role: "user", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.GetChat(ctx, "alice", "bad"); err == nil {
		t.Fatal("the corrupt chat should not load")
	}
}

// A message a request took (delivered, unconfirmed) before a crash comes back
// as pending, in its original place.
func TestJSONReopenRestoresUnconfirmed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	newChat(t, first, "a")
	for _, txt := range []string{"m1", "m2"} {
		if _, err := first.AddPending(ctx, "alice", "a", txt); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.ClaimTurn(ctx, "alice", "a", "r1"); err != nil {
		t.Fatal(err)
	}
	taken, err := first.TakePending(ctx, "alice", "a")
	if err != nil || len(taken) != 2 {
		t.Fatalf("take = %v, %v", taken, err)
	}
	if err := first.ConfirmPending(ctx, "alice", "a", []string{taken[0].ID}); err != nil {
		t.Fatal(err)
	}
	second, err := store.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := second.Pending(ctx, "alice", "a")
	if err != nil || len(ps) != 1 || ps[0].Text != "m2" {
		t.Fatalf("pending after reopen = %+v, %v (want only the unconfirmed m2)", ps, err)
	}
}
