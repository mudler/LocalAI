package studio_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

// countingStore counts the calls that write a transcript.
type countingStore struct {
	store.Store
	single, batches atomic.Int32
}

func (c *countingStore) AppendEntry(ctx context.Context, id string, e session.Entry) (int64, error) {
	c.single.Add(1)
	return c.Store.AppendEntry(ctx, id, e)
}

func (c *countingStore) AppendEntries(ctx context.Context, id string, es []session.Entry) error {
	c.batches.Add(1)
	return c.Store.AppendEntries(ctx, id, es)
}

// A long history must not cost one write per message: on SQLite each write
// takes the lock of the whole database, and logins wait behind it.
func TestAnImportedChatIsWrittenInBatches(t *testing.T) {
	js, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	cs := &countingStore{Store: js}
	svc, err := studio.New(studio.Deps{Store: cs, Provider: provider.New(), StateDir: t.TempDir(), Harness: studiotest.NewGateHarness()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })

	const n = 1000
	entries := make([]studio.ImportedEntry, n)
	for i := range entries {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		entries[i] = studio.ImportedEntry{Role: role, Text: fmt.Sprintf("message %d", i), At: time.Unix(1700000000+int64(i), 0)}
	}
	rec, err := svc.ImportChat(context.Background(), alice, "Long", "m", time.Unix(1700000000, 0), time.Unix(1700001000, 0), entries)
	if err != nil {
		t.Fatal(err)
	}
	if cs.single.Load() != 0 || cs.batches.Load() > n/100 {
		t.Fatalf("the import made %d single writes and %d batches for %d messages", cs.single.Load(), cs.batches.Load(), n)
	}
	got, err := cs.Entries(context.Background(), rec.ID, 0, 0)
	if err != nil || len(got) != n || got[0].Text != "message 0" || got[n-1].Text != "message 999" {
		t.Fatalf("stored %d entries, %v", len(got), err)
	}
}
