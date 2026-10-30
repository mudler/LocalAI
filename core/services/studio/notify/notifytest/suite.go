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
	return notify.Notification{Principal: p, Kind: notify.KindRunEnded, Status: notify.StatusOK, Title: title, Link: "/app/chats/" + title, Created: at}
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
