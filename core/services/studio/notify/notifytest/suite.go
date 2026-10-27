// Package notifytest is the contract suite every notify.Store passes.
package notifytest

import (
	"context"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/notify"
)

// NewStore returns an empty store.
type NewStore func(t *testing.T) notify.Store

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

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Run checks the store.
func Run(t *testing.T, newStore NewStore) {
	ctx := context.Background()
	t.Run("AddListNewestFirst", func(t *testing.T) {
		s := newStore(t)
		var prev int64
		for _, ti := range []string{"a", "b", "c"} {
			id, err := s.Add(ctx, note("u1", ti, t0))
			if err != nil || id <= prev {
				t.Fatalf("id %d after %d, err %v", id, prev, err)
			}
			prev = id
		}
		got, err := s.List(ctx, "u1", false, 0)
		if err != nil || !same(titles(got), []string{"c", "b", "a"}) {
			t.Fatalf("got %v, %v", titles(got), err)
		}
		if got[0].Kind != notify.KindRunEnded || got[0].Status != notify.StatusOK || got[0].Link != "/app/chats/c" || !got[0].Created.Equal(t0) || got[0].Read {
			t.Fatalf("fields = %+v", got[0])
		}
		lim, _ := s.List(ctx, "u1", false, 2)
		if !same(titles(lim), []string{"c", "b"}) {
			t.Fatalf("limit 2 gave %v", titles(lim))
		}
	})
	t.Run("ReadStates", func(t *testing.T) {
		s := newStore(t)
		a, _ := s.Add(ctx, note("u1", "a", t0))
		_, _ = s.Add(ctx, note("u1", "b", t0))
		_, _ = s.Add(ctx, note("u1", "c", t0))
		if n, _ := s.Count(ctx, "u1"); n != 3 {
			t.Fatalf("unread = %d", n)
		}
		if err := s.MarkRead(ctx, "u1", []int64{a}); err != nil {
			t.Fatal(err)
		}
		un, _ := s.List(ctx, "u1", true, 0)
		if !same(titles(un), []string{"c", "b"}) {
			t.Fatalf("unread list = %v", titles(un))
		}
		if err := s.MarkChatRead(ctx, "u1", "/app/chats/b"); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.Count(ctx, "u1"); n != 1 {
			t.Fatalf("unread after chat read = %d", n)
		}
		if err := s.MarkAllRead(ctx, "u1"); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.Count(ctx, "u1"); n != 0 {
			t.Fatalf("unread after all = %d", n)
		}
	})
	t.Run("DeleteAndOtherPrincipal", func(t *testing.T) {
		s := newStore(t)
		a, _ := s.Add(ctx, note("u1", "a", t0))
		b, _ := s.Add(ctx, note("u2", "b", t0))
		_ = s.MarkRead(ctx, "u1", []int64{b})
		_ = s.Delete(ctx, "u1", []int64{b})
		_ = s.MarkChatRead(ctx, "u1", "/app/chats/b")
		_ = s.MarkAllRead(ctx, "u1")
		if got, _ := s.List(ctx, "u2", true, 0); len(got) != 1 {
			t.Fatalf("u2 lost or read its notification: %+v", got)
		}
		if err := s.Delete(ctx, "u1", []int64{a}); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.List(ctx, "u1", false, 0); len(got) != 0 {
			t.Fatalf("u1 after delete: %+v", got)
		}
		if got, _ := s.List(ctx, "nobody", false, 0); len(got) != 0 {
			t.Fatalf("a stranger sees %+v", got)
		}
	})
	t.Run("TrimToCount", func(t *testing.T) {
		s := newStore(t)
		for i := 0; i < notify.MaxPerUser+5; i++ {
			if _, err := s.Add(ctx, note("u1", "n", t0.Add(time.Duration(i)*time.Second))); err != nil {
				t.Fatal(err)
			}
		}
		_, _ = s.Add(ctx, note("u2", "other", t0))
		got, _ := s.List(ctx, "u1", false, 0)
		if len(got) != notify.MaxPerUser {
			t.Fatalf("kept %d, want %d", len(got), notify.MaxPerUser)
		}
		if other, _ := s.List(ctx, "u2", false, 0); len(other) != 1 {
			t.Fatalf("trimming u1 touched u2: %+v", other)
		}
	})
	t.Run("TrimByAge", func(t *testing.T) {
		s := newStore(t)
		_, _ = s.Add(ctx, note("u1", "old", t0))
		_, _ = s.Add(ctx, note("u1", "recent", t0.Add(notify.MaxAge-time.Hour)))
		if got, _ := s.List(ctx, "u1", false, 0); len(got) != 2 {
			t.Fatalf("within the age limit: %v", titles(got))
		}
		_, _ = s.Add(ctx, note("u1", "new", t0.Add(notify.MaxAge+time.Hour)))
		got, _ := s.List(ctx, "u1", false, 0)
		if !same(titles(got), []string{"new", "recent"}) {
			t.Fatalf("after the age limit: %v", titles(got))
		}
	})
}

// Seed stores one notification; CheckSeed finds it in another store on the same data.
func Seed(t *testing.T, s notify.Store) {
	t.Helper()
	if _, err := s.Add(context.Background(), note("u1", "seeded", t0)); err != nil {
		t.Fatal(err)
	}
}

// CheckSeed checks what Seed wrote, and that new ids keep growing.
func CheckSeed(t *testing.T, s notify.Store) {
	t.Helper()
	got, err := s.List(context.Background(), "u1", false, 0)
	if err != nil || len(got) != 1 || got[0].Title != "seeded" {
		t.Fatalf("seed: %+v %v", got, err)
	}
	id, err := s.Add(context.Background(), note("u1", "after", t0))
	if err != nil || id <= got[0].ID {
		t.Fatalf("id %d after reopen, was %d: %v", id, got[0].ID, err)
	}
}
