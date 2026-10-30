package store_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/store/storetest"
)

func chatFile(dir, id, ext string) string {
	return filepath.Join(dir, "chats", base64.RawURLEncoding.EncodeToString([]byte(id))+ext)
}

func newChat(s store.Store, id string) {
	now := time.Now().UTC()
	Expect(s.CreateChat(context.Background(), session.ChatRecord{ID: id, Principal: "alice", Title: id, Created: now, Updated: now})).To(Succeed())
}

var _ = Describe("the JSON store", func() {
	Describe("in memory", func() {
		storetest.Run(func() store.Store {
			s, err := store.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		storetest.Run(func() store.Store {
			s, err := store.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	// A store reopened on the same directory sees what the first one wrote: this
	// is what makes the JSON store a real persistence mode and not only a cache.
	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		storetest.Seed(first)
		second, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		storetest.CheckSeed(second)
	})

	// A crash can leave half a line at the end of a transcript. The store must
	// still open, drop the half line, and append on a clean line.
	It("opens with a torn transcript line", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		first, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		newChat(first, "a")
		for _, txt := range []string{"one", "two"} {
			Expect(first.AppendEntry(ctx, "a", session.Entry{Role: "user", Text: txt})).Error().NotTo(HaveOccurred())
		}
		f, err := os.OpenFile(chatFile(dir, "a", ".entries"), os.O_APPEND|os.O_WRONLY, 0o600)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.WriteString(`{"Seq":3,"Role":"us`)
		Expect(err).NotTo(HaveOccurred())
		_ = f.Close()

		second, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred(), "NewJSON with a torn transcript")
		es, _ := second.Entries(ctx, "a", 0, 10)
		Expect(es).To(HaveLen(2), "want the 2 complete ones")
		Expect(second.AppendEntry(ctx, "a", session.Entry{Role: "user", Text: "three"})).Error().NotTo(HaveOccurred())
		third, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred(), "reopen after append")
		es, _ = third.Entries(ctx, "a", 0, 10)
		Expect(es).To(HaveLen(3), "entries after append = %+v", es)
		Expect(es[2].Text).To(Equal("three"))
		Expect(es[2].Seq).To(BeEquivalentTo(3))
	})

	// One damaged record must not take the other chats with it.
	It("skips a corrupt record", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		first, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		newChat(first, "good")
		newChat(first, "bad")
		Expect(os.WriteFile(chatFile(dir, "bad", ".json"), []byte(`{"rec":{"ID":"ba`), 0o600)).To(Succeed())
		second, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred(), "NewJSON with a corrupt record")
		_, err = second.GetChat(ctx, "alice", "good")
		Expect(err).NotTo(HaveOccurred(), "good chat")
		Expect(second.AppendEntry(ctx, "good", session.Entry{Role: "user", Text: "x"})).Error().NotTo(HaveOccurred())
		_, err = second.GetChat(ctx, "alice", "bad")
		Expect(err).To(HaveOccurred(), "the corrupt chat should not load")
	})

	// A message a request took (delivered, unconfirmed) before a crash comes back
	// as pending, in its original place.
	It("restores unconfirmed messages on reopen", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		first, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		newChat(first, "a")
		for _, txt := range []string{"m1", "m2"} {
			Expect(first.AddPending(ctx, "alice", "a", txt)).Error().NotTo(HaveOccurred())
		}
		Expect(first.ClaimTurn(ctx, "alice", "a", "r1")).To(Succeed())
		taken, err := first.TakePending(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(taken).To(HaveLen(2))
		Expect(first.ConfirmPending(ctx, "alice", "a", []string{taken[0].ID})).To(Succeed())
		second, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		ps, err := second.Pending(ctx, "alice", "a")
		Expect(err).NotTo(HaveOccurred())
		Expect(ps).To(HaveLen(1), "pending after reopen = %+v (want only the unconfirmed m2)", ps)
		Expect(ps[0].Text).To(Equal("m2"))
	})

	Describe("harness state file", func() {
		var (
			ctx = context.Background()
			dir string
			st  store.Store
		)
		BeforeEach(func() {
			dir = GinkgoT().TempDir()
			var err error
			st, err = store.NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			newChat(st, "a")
		})
		update := func(change func(*session.ChatRecord)) {
			rec, err := st.GetChat(ctx, "alice", "a")
			Expect(err).NotTo(HaveOccurred())
			change(&rec)
			Expect(st.UpdateChat(ctx, rec)).To(Succeed())
		}
		record := func() map[string]any {
			raw, err := os.ReadFile(chatFile(dir, "a", ".json"))
			Expect(err).NotTo(HaveOccurred())
			var m map[string]any
			Expect(json.Unmarshal(raw, &m)).To(Succeed())
			return m["rec"].(map[string]any)
		}

		It("keeps the state out of the record file and loads it on reopen", func() {
			update(func(r *session.ChatRecord) { r.HarnessState = []byte("history-1") })
			Expect(record()["HarnessState"]).To(BeNil(), "the record file holds the harness state")
			Expect(os.ReadFile(chatFile(dir, "a", ".1.state"))).To(Equal([]byte("history-1")))
			again, err := store.NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			rec, err := again.GetChat(ctx, "alice", "a")
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HarnessState).To(Equal([]byte("history-1")))
		})

		It("writes the state file only when the state changes", func() {
			update(func(r *session.ChatRecord) { r.HarnessState = []byte("history-1") })
			update(func(r *session.ChatRecord) { r.Title = "renamed" })
			_, err := os.Stat(chatFile(dir, "a", ".2.state"))
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue(), "a title change wrote the state again")
			update(func(r *session.ChatRecord) { r.HarnessState = []byte("history-2") })
			Expect(os.ReadFile(chatFile(dir, "a", ".2.state"))).To(Equal([]byte("history-2")))
			_, err = os.Stat(chatFile(dir, "a", ".1.state"))
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue(), "the old generation was kept")
			rec, err := st.GetChat(ctx, "alice", "a")
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Title).To(Equal("renamed"))
			Expect(rec.HarnessState).To(Equal([]byte("history-2")))
		})

		It("moves the inline state of an older record to a state file", func() {
			now := time.Now().UTC()
			legacy, err := json.Marshal(map[string]any{"rec": session.ChatRecord{ID: "old", Principal: "alice", Title: "old",
				Created: now, Updated: now, HarnessState: []byte("inline history")}})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(chatFile(dir, "old", ".json"), legacy, 0o600)).To(Succeed())
			again, err := store.NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			rec, err := again.GetChat(ctx, "alice", "old")
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HarnessState).To(Equal([]byte("inline history")))
			Expect(os.ReadFile(chatFile(dir, "old", ".1.state"))).To(Equal([]byte("inline history")))
			raw, err := os.ReadFile(chatFile(dir, "old", ".json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString([]byte("inline history"))))
		})

		It("drops a state file that no record names, as a crash leaves it", func() {
			update(func(r *session.ChatRecord) { r.HarnessState = []byte("history-1") })
			Expect(os.WriteFile(chatFile(dir, "a", ".2.state"), []byte("never saved"), 0o600)).To(Succeed())
			again, err := store.NewJSON(dir)
			Expect(err).NotTo(HaveOccurred())
			rec, err := again.GetChat(ctx, "alice", "a")
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HarnessState).To(Equal([]byte("history-1")))
			_, err = os.Stat(chatFile(dir, "a", ".2.state"))
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
		})

		It("deletes the state file with the chat", func() {
			update(func(r *session.ChatRecord) { r.HarnessState = []byte("history-1") })
			Expect(st.DeleteChat(ctx, "alice", "a")).To(Succeed())
			_, err := os.Stat(chatFile(dir, "a", ".1.state"))
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
		})
	})

	// Each chat has its own lock: the operations on one chat serialize, and a
	// delete that races them leaves no half-written chat behind.
	It("serializes concurrent changes to one chat", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		st, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		const n = 40
		var wg sync.WaitGroup
		for _, id := range []string{"a", "b"} {
			newChat(st, id)
			for i := range n {
				wg.Add(1)
				go func(id string, i int) {
					defer GinkgoRecover()
					defer wg.Done()
					_, err := st.AddPending(ctx, "alice", id, fmt.Sprintf("m%d", i))
					Expect(err).NotTo(HaveOccurred())
				}(id, i)
			}
		}
		wg.Wait()
		again, err := store.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		for _, id := range []string{"a", "b"} {
			ps, err := again.Pending(ctx, "alice", id)
			Expect(err).NotTo(HaveOccurred())
			Expect(ps).To(HaveLen(n), "a concurrent change to %s was lost", id)
			seen := map[string]bool{}
			for _, p := range ps {
				seen[p.ID] = true
			}
			Expect(seen).To(HaveLen(n), "two messages of %s got one ID", id)
		}
	})

	It("reports a chat deleted during other changes as not found", func() {
		ctx := context.Background()
		st, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		newChat(st, "a")
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				if _, err := st.AddPending(ctx, "alice", "a", "m"); err != nil {
					Expect(err).To(MatchError(session.ErrNotFound))
				}
			}()
		}
		Expect(st.DeleteChat(ctx, "alice", "a")).To(Succeed())
		wg.Wait()
		_, err = st.GetChat(ctx, "alice", "a")
		Expect(err).To(MatchError(session.ErrNotFound))
		Expect(st.ListChats(ctx, "alice", session.ChatCursor{}, 0)).To(BeEmpty())
	})
})
