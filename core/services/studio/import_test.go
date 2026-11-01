package studio_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
var _ = Describe("import", func() {
	It("an imported chat is written in batches", func() {
		js, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		cs := &countingStore{Store: js}
		svc, err := studio.New(studio.Deps{Store: cs, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })

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
		Expect(err).NotTo(HaveOccurred())
		Expect(cs.single.Load()).To(BeNumerically("==", 0), "the import made %d single writes and %d batches for %d messages", cs.single.Load(), cs.batches.Load(), n)
		Expect(cs.batches.Load()).To(BeNumerically("<=", n/100), "the import made %d single writes and %d batches for %d messages", cs.single.Load(), cs.batches.Load(), n)
		got, err := cs.Entries(context.Background(), rec.ID, 0, 0)
		Expect(err).NotTo(HaveOccurred(), "stored %d entries, %v", len(got), err)
		Expect(got).To(HaveLen(n), "stored %d entries, %v", len(got), err)
		Expect(got[0].Text).To(Equal("message 0"), "stored %d entries, %v", len(got), err)
		Expect(got[n-1].Text).To(Equal("message 999"), "stored %d entries, %v", len(got), err)
	})
})
