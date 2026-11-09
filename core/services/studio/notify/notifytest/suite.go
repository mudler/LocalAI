// Package notifytest is the contract suite every notify.Store passes.
package notifytest

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/notify"
)

// NewStore returns an empty store. It runs inside a spec, so it may use
// GinkgoT().TempDir() and DeferCleanup.
type NewStore func() notify.Store

var t0 = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func note(p, title string, at time.Time) notify.Notification {
	return notify.Notification{Principal: p, Kind: notify.KindRunEnded, Status: notify.StatusOK, Source: notify.SourceTrigger, Title: title, Link: "/app/chats/" + title, Created: at}
}

func titles(ns []notify.Notification) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Title
	}
	return out
}

// Run registers the contract specs for the stores newStore returns. Call it
// at the top level of a Describe in the test package of an implementation.
func Run(newStore NewStore) {
	ctx := context.Background()
	It("adds notifications and lists the newest first", func() {
		s := newStore()
		var prev int64
		for _, ti := range []string{"a", "b", "c"} {
			id, err := s.Add(ctx, note("u1", ti, t0))
			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(BeNumerically(">", prev), "id %d after %d", id, prev)
			prev = id
		}
		got, err := s.List(ctx, "u1", false, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(titles(got)).To(Equal([]string{"c", "b", "a"}))
		Expect(got[0].Kind).To(Equal(notify.KindRunEnded))
		Expect(got[0].Status).To(Equal(notify.StatusOK))
		Expect(got[0].Source).To(Equal(notify.SourceTrigger))
		Expect(got[0].Link).To(Equal("/app/chats/c"))
		Expect(got[0].Created.Equal(t0)).To(BeTrue())
		Expect(got[0].Read).To(BeFalse())
		lim, _ := s.List(ctx, "u1", false, 2)
		Expect(titles(lim)).To(Equal([]string{"c", "b"}), "limit 2")
	})
	It("tracks the read states", func() {
		s := newStore()
		a, _ := s.Add(ctx, note("u1", "a", t0))
		_, _ = s.Add(ctx, note("u1", "b", t0))
		_, _ = s.Add(ctx, note("u1", "c", t0))
		n, _ := s.Count(ctx, "u1")
		Expect(n).To(BeEquivalentTo(3), "unread")
		Expect(s.MarkRead(ctx, "u1", []int64{a})).To(Succeed())
		un, _ := s.List(ctx, "u1", true, 0)
		Expect(titles(un)).To(Equal([]string{"c", "b"}), "unread list")
		Expect(s.MarkChatRead(ctx, "u1", "/app/chats/b")).To(Succeed())
		n, _ = s.Count(ctx, "u1")
		Expect(n).To(BeEquivalentTo(1), "unread after chat read")
		Expect(s.MarkAllRead(ctx, "u1")).To(Succeed())
		n, _ = s.Count(ctx, "u1")
		Expect(n).To(BeEquivalentTo(0), "unread after all")
	})
	It("deletes and leaves the other principals alone", func() {
		s := newStore()
		a, _ := s.Add(ctx, note("u1", "a", t0))
		b, _ := s.Add(ctx, note("u2", "b", t0))
		_ = s.MarkRead(ctx, "u1", []int64{b})
		_ = s.Delete(ctx, "u1", []int64{b})
		_ = s.MarkChatRead(ctx, "u1", "/app/chats/b")
		_ = s.MarkAllRead(ctx, "u1")
		got, _ := s.List(ctx, "u2", true, 0)
		Expect(got).To(HaveLen(1), "u2 lost or read its notification")
		Expect(s.Delete(ctx, "u1", []int64{a})).To(Succeed())
		got, _ = s.List(ctx, "u1", false, 0)
		Expect(got).To(BeEmpty(), "u1 after delete")
		got, _ = s.List(ctx, "nobody", false, 0)
		Expect(got).To(BeEmpty(), "a stranger sees notifications")
	})
	It("deletes every notification of a principal, or only the read ones", func() {
		s := newStore()
		a, _ := s.Add(ctx, note("u1", "a", t0))
		_, _ = s.Add(ctx, note("u1", "b", t0))
		_, _ = s.Add(ctx, note("u2", "other", t0))
		Expect(s.MarkRead(ctx, "u1", []int64{a})).To(Succeed())
		n, err := s.DeleteAll(ctx, "u1", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1), "removed read ones")
		got, _ := s.List(ctx, "u1", false, 0)
		Expect(titles(got)).To(Equal([]string{"b"}))
		n, err = s.DeleteAll(ctx, "u1", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1), "removed the rest")
		n, _ = s.DeleteAll(ctx, "u1", false)
		Expect(n).To(BeZero(), "an empty inbox")
		got, _ = s.List(ctx, "u2", false, 0)
		Expect(got).To(HaveLen(1), "u1's clear reached u2")
		n, _ = s.DeleteAll(ctx, "nobody", false)
		Expect(n).To(BeZero())
	})
	It("trims to a count", func() {
		s := newStore()
		for i := range notify.MaxPerUser + 5 {
			_, err := s.Add(ctx, note("u1", "n", t0.Add(time.Duration(i)*time.Second)))
			Expect(err).NotTo(HaveOccurred())
		}
		_, _ = s.Add(ctx, note("u2", "other", t0))
		got, _ := s.List(ctx, "u1", false, 0)
		Expect(got).To(HaveLen(notify.MaxPerUser))
		other, _ := s.List(ctx, "u2", false, 0)
		Expect(other).To(HaveLen(1), "trimming u1 touched u2")
	})
	It("trims by age", func() {
		s := newStore()
		_, _ = s.Add(ctx, note("u1", "old", t0))
		_, _ = s.Add(ctx, note("u1", "recent", t0.Add(notify.MaxAge-time.Hour)))
		got, _ := s.List(ctx, "u1", false, 0)
		Expect(got).To(HaveLen(2), "within the age limit: %v", titles(got))
		_, _ = s.Add(ctx, note("u1", "new", t0.Add(notify.MaxAge+time.Hour)))
		got, _ = s.List(ctx, "u1", false, 0)
		Expect(titles(got)).To(Equal([]string{"new", "recent"}), "after the age limit")
	})
}

func digest(p, title string, ids ...string) notify.Notification {
	return notify.Notification{Principal: p, Key: "updates.digest", Kind: notify.KindNeedsAttention, Status: notify.StatusOK,
		Source: notify.SourceSystem, Title: title, Link: "/app/updates", Code: "updates.digest",
		Params: map[string]any{"count": len(ids)}, Ids: ids, Created: t0}
}

// RunKeyed registers the specs of the keyed rows: Upsert, the hidden state and
// the exemption of set rows from the trims.
func RunKeyed(newStore NewStore) {
	ctx := context.Background()
	It("creates a keyed row with its code and params and edits it quietly when ids leave", func() {
		s := newStore()
		got, res, err := s.Upsert(ctx, digest("u1", "2 updates", "a", "b"), notify.ModeSet)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(notify.ResultCreated))
		first := got.ID
		list, _ := s.List(ctx, "u1", false, 0)
		Expect(list).To(HaveLen(1))
		Expect(list[0].Code).To(Equal("updates.digest"))
		Expect(list[0].Params).To(HaveKeyWithValue("count", BeNumerically("==", 2)))
		Expect(list[0].Key).To(Equal("updates.digest"))
		Expect(list[0].Ids).To(ConsistOf("a", "b"))

		_, res, _ = s.Upsert(ctx, digest("u1", "2 updates", "b", "a"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultUnchanged), "same ids, same text")

		Expect(s.MarkRead(ctx, "u1", []int64{first})).To(Succeed())
		got, res, _ = s.Upsert(ctx, digest("u1", "1 update", "a"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultUpdated), "fewer ids")
		Expect(got.ID).To(Equal(first), "an edit keeps the row")
		list, _ = s.List(ctx, "u1", false, 0)
		Expect(list).To(HaveLen(1))
		Expect(list[0].Title).To(Equal("1 update"))
		Expect(list[0].Read).To(BeTrue(), "a quiet edit does not make it unread")
		Expect(list[0].Ids).To(ConsistOf("a"))
	})
	It("revives on an id the row did not show: unread, unhidden, newer than the others", func() {
		s := newStore()
		first, _, _ := s.Upsert(ctx, digest("u1", "1 update", "a"), notify.ModeSet)
		_, _ = s.Add(ctx, note("u1", "later", t0))
		Expect(s.MarkRead(ctx, "u1", []int64{first.ID})).To(Succeed())
		Expect(s.Delete(ctx, "u1", []int64{first.ID})).To(Succeed())
		got, res, _ := s.Upsert(ctx, digest("u1", "2 updates", "a", "b"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultRevived))
		Expect(got.ID).To(BeNumerically(">", first.ID))
		list, _ := s.List(ctx, "u1", false, 0)
		Expect(titles(list)).To(Equal([]string{"2 updates", "later"}), "the revived row is the newest")
		Expect(list[0].Read).To(BeFalse())
		n, _ := s.Count(ctx, "u1")
		Expect(n).To(Equal(2))
	})
	It("keeps a deleted keyed row hidden until a new id appears", func() {
		s := newStore()
		first, _, _ := s.Upsert(ctx, digest("u1", "2 updates", "a", "b"), notify.ModeSet)
		Expect(s.Delete(ctx, "u1", []int64{first.ID})).To(Succeed())
		list, _ := s.List(ctx, "u1", false, 0)
		Expect(list).To(BeEmpty(), "the dismissed row is not listed")
		n, _ := s.Count(ctx, "u1")
		Expect(n).To(BeZero())

		_, res, _ := s.Upsert(ctx, digest("u1", "2 updates", "a", "b"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultUnchanged), "the same ids after the dismissal")
		_, res, _ = s.Upsert(ctx, digest("u1", "1 update", "b"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultUpdated), "a smaller set after the dismissal")
		list, _ = s.List(ctx, "u1", false, 0)
		Expect(list).To(BeEmpty(), "still hidden after the quiet edit")
		_, res, _ = s.Upsert(ctx, digest("u1", "2 updates", "b", "c"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultRevived), "a new id")
		list, _ = s.List(ctx, "u1", false, 0)
		Expect(titles(list)).To(Equal([]string{"2 updates"}))
		Expect(list[0].Read).To(BeFalse())
	})
	It("removes the row when the set is empty and treats the next one as new", func() {
		s := newStore()
		_, res, _ := s.Upsert(ctx, digest("u1", "gone"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultUnchanged), "an empty set with no row")
		first, _, _ := s.Upsert(ctx, digest("u1", "1 update", "a"), notify.ModeSet)
		Expect(s.Delete(ctx, "u1", []int64{first.ID})).To(Succeed())
		_, res, _ = s.Upsert(ctx, digest("u1", "gone"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultRemoved), "a hidden row goes too")
		_, res, _ = s.Upsert(ctx, digest("u1", "1 update", "a"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultCreated), "the same id again is new after the removal")
	})
	It("hides keyed rows on clear-all and delete-read, and removes plain ones", func() {
		s := newStore()
		d, _, _ := s.Upsert(ctx, digest("u1", "1 update", "a"), notify.ModeSet)
		_, _ = s.Add(ctx, note("u1", "plain", t0))
		Expect(s.MarkRead(ctx, "u1", []int64{d.ID})).To(Succeed())
		n, err := s.DeleteAll(ctx, "u1", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1), "only the read keyed row")
		list, _ := s.List(ctx, "u1", false, 0)
		Expect(titles(list)).To(Equal([]string{"plain"}))
		n, _ = s.DeleteAll(ctx, "u1", false)
		Expect(n).To(Equal(1))
		_, res, _ := s.Upsert(ctx, digest("u1", "1 update", "a"), notify.ModeSet)
		Expect(res).To(Equal(notify.ResultUnchanged), "still remembered after clear-all")
	})
	It("keeps a row for each principal", func() {
		s := newStore()
		_, r1, _ := s.Upsert(ctx, digest("u1", "x", "a"), notify.ModeSet)
		_, r2, _ := s.Upsert(ctx, digest("u2", "x", "a"), notify.ModeSet)
		Expect([]notify.UpsertResult{r1, r2}).To(Equal([]notify.UpsertResult{notify.ResultCreated, notify.ResultCreated}))
		a, _ := s.List(ctx, "u1", false, 0)
		Expect(a).To(HaveLen(1))
		Expect(s.Delete(ctx, "u2", []int64{a[0].ID})).To(Succeed(), "another principal's id is ignored")
		a, _ = s.List(ctx, "u1", false, 0)
		Expect(a).To(HaveLen(1))
	})
	It("does not trim a set row by count or by age", func() {
		s := newStore()
		_, _, _ = s.Upsert(ctx, digest("u1", "set", "a"), notify.ModeSet)
		for i := range notify.MaxPerUser + 5 {
			_, err := s.Add(ctx, note("u1", "n", t0.Add(notify.MaxAge+time.Duration(i)*time.Second)))
			Expect(err).NotTo(HaveOccurred())
		}
		list, _ := s.List(ctx, "u1", false, 0)
		Expect(list).To(HaveLen(notify.MaxPerUser + 1))
		Expect(list[len(list)-1].Title).To(Equal("set"))
	})
	It("posts a once row one time, even after it was dismissed", func() {
		s := newStore()
		o := digest("u1", "2 updated", "ignored")
		o.Key = "updates.result.backends.abc"
		got, res, _ := s.Upsert(ctx, o, notify.ModeOnce)
		Expect(res).To(Equal(notify.ResultCreated))
		Expect(got.Ids).To(BeEmpty(), "a once row carries no set")
		_, res, _ = s.Upsert(ctx, o, notify.ModeOnce)
		Expect(res).To(Equal(notify.ResultUnchanged))
		Expect(s.Delete(ctx, "u1", []int64{got.ID})).To(Succeed())
		_, res, _ = s.Upsert(ctx, o, notify.ModeOnce)
		Expect(res).To(Equal(notify.ResultUnchanged), "dismissed, not forgotten")
		list, _ := s.List(ctx, "u1", false, 0)
		Expect(list).To(BeEmpty())
		o.Key = "updates.result.backends.def"
		_, res, _ = s.Upsert(ctx, o, notify.ModeOnce)
		Expect(res).To(Equal(notify.ResultCreated), "another key")
	})
	It("needs a key", func() {
		_, _, err := newStore().Upsert(ctx, note("u1", "no key", t0), notify.ModeSet)
		Expect(err).To(HaveOccurred())
	})
}

// Seed stores one notification; CheckSeed finds it in another store on the same data.
func Seed(s notify.Store) {
	_, err := s.Add(context.Background(), note("u1", "seeded", t0))
	Expect(err).NotTo(HaveOccurred())
}

// CheckSeed checks what Seed wrote, and that new ids keep growing.
func CheckSeed(s notify.Store) {
	got, err := s.List(context.Background(), "u1", false, 0)
	Expect(err).NotTo(HaveOccurred())
	Expect(got).To(HaveLen(1))
	Expect(got[0].Title).To(Equal("seeded"))
	id, err := s.Add(context.Background(), note("u1", "after", t0))
	Expect(err).NotTo(HaveOccurred())
	Expect(id).To(BeNumerically(">", got[0].ID), "id after reopen")
}
