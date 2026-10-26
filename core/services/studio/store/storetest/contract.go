// Package storetest is the conformance suite for store.Store. Every
// implementation passes it, so the JSON and GORM stores behave the same.
package storetest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// New returns an empty store for one test.
type New func(t *testing.T) store.Store

var t0 = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func chat(t *testing.T, s store.Store, id, principal string) {
	t.Helper()
	if err := s.CreateChat(ctx(t), session.ChatRecord{ID: id, Principal: principal, Title: "New chat", Created: t0, Updated: t0}); err != nil {
		t.Fatal(err)
	}
}

func texts(ps []store.Pending) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Text)
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

// Run runs the suite against stores from newStore.
func Run(t *testing.T, newStore New) {
	t.Helper()

	// The loop's own transcript contract.
	sessiontest.RunStoreSuite(t, func(t *testing.T) session.Store { return newStore(t) })

	t.Run("skill switches round-trip, survive a chat update and belong to the owner", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		if err := s.SetSkillsOff(ctx(t), "alice", "c1", []string{"review", "notes"}); err != nil {
			t.Fatal(err)
		}
		rec, err := s.GetChat(ctx(t), "alice", "c1")
		if err != nil || len(rec.SkillsOff) != 2 || rec.SkillsOff[0] != "review" || rec.SkillsOff[1] != "notes" {
			t.Fatalf("SkillsOff = %v, %v, want review and notes", rec.SkillsOff, err)
		}
		// The loop saves its own copy of the record at the end of a turn. That
		// copy has no switches in it and must not wipe them.
		stale := rec
		stale.SkillsOff = nil
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		if err := s.UpdateChat(ctx(t), stale); err != nil {
			t.Fatal(err)
		}
		rec, _ = s.GetChat(ctx(t), "alice", "c1")
		if rec.Title != "Renamed" || len(rec.SkillsOff) != 2 {
			t.Fatalf("after UpdateChat: title %q, SkillsOff %v, want the switches kept", rec.Title, rec.SkillsOff)
		}
		if err := s.SetSkillsOff(ctx(t), "bob", "c1", []string{"x"}); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("another principal's SetSkillsOff = %v, want ErrNotFound", err)
		}
		if err := s.SetSkillsOff(ctx(t), "alice", "c1", nil); err != nil {
			t.Fatal(err)
		}
		if rec, _ = s.GetChat(ctx(t), "alice", "c1"); len(rec.SkillsOff) != 0 {
			t.Fatalf("after clearing: %v", rec.SkillsOff)
		}
	})

	t.Run("kind, parent, model, status and summary round-trip", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "parent", "alice")
		rec := session.ChatRecord{ID: "child", Principal: "alice", Title: "Sub", Created: t0, Updated: t0, Kind: session.KindSubAgent, ParentID: "parent", Model: "qwen3"}
		if err := s.CreateChat(ctx(t), rec); err != nil {
			t.Fatal(err)
		}
		rec.Status, rec.Summary, rec.Updated = "done", "short summary", t0.Add(time.Minute)
		if err := s.UpdateChat(ctx(t), rec); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetChat(ctx(t), "alice", "child")
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != session.KindSubAgent || got.ParentID != "parent" || got.Model != "qwen3" || got.Status != "done" || got.Summary != "short summary" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("pending messages are taken once, in send order", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		for _, x := range []string{"one", "two", "three"} {
			if _, err := s.AddPending(ctx(t), "alice", "c1", x); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.TakePending(ctx(t), "alice", "c1")
		if err != nil {
			t.Fatal(err)
		}
		if !same(texts(got), []string{"one", "two", "three"}) {
			t.Fatalf("first take = %v", texts(got))
		}
		again, err := s.TakePending(ctx(t), "alice", "c1")
		if err != nil || len(again) != 0 {
			t.Fatalf("second take = %v, %v; want nothing", texts(again), err)
		}
		left, err := s.Pending(ctx(t), "alice", "c1")
		if err != nil || len(left) != 0 {
			t.Fatalf("Pending after take = %v, %v; want nothing", texts(left), err)
		}
	})

	t.Run("edit and cancel work until the message is taken", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		a, _ := s.AddPending(ctx(t), "alice", "c1", "keep")
		b, _ := s.AddPending(ctx(t), "alice", "c1", "drop")
		c, _ := s.AddPending(ctx(t), "alice", "c1", "typo")
		if err := s.EditPending(ctx(t), "alice", "c1", c.ID, "fixed"); err != nil {
			t.Fatal(err)
		}
		if err := s.CancelPending(ctx(t), "alice", "c1", b.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := s.TakePending(ctx(t), "alice", "c1")
		if !same(texts(got), []string{"keep", "fixed"}) {
			t.Fatalf("taken = %v", texts(got))
		}
		if err := s.EditPending(ctx(t), "alice", "c1", a.ID, "late"); !errors.Is(err, store.ErrDelivered) {
			t.Fatalf("edit after take = %v, want ErrDelivered", err)
		}
		if err := s.CancelPending(ctx(t), "alice", "c1", a.ID); !errors.Is(err, store.ErrDelivered) {
			t.Fatalf("cancel after take = %v, want ErrDelivered", err)
		}
	})

	t.Run("restore puts taken messages back in their original order", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		_, _ = s.AddPending(ctx(t), "alice", "c1", "one")
		_, _ = s.AddPending(ctx(t), "alice", "c1", "two")
		taken, _ := s.TakePending(ctx(t), "alice", "c1")
		_, _ = s.AddPending(ctx(t), "alice", "c1", "three")
		var ids []string
		for _, p := range taken {
			ids = append(ids, p.ID)
		}
		if err := s.RestorePending(ctx(t), "alice", "c1", ids); err != nil {
			t.Fatal(err)
		}
		got, _ := s.TakePending(ctx(t), "alice", "c1")
		if !same(texts(got), []string{"one", "two", "three"}) {
			t.Fatalf("after restore = %v", texts(got))
		}
	})

	t.Run("another principal sees ErrNotFound for every call", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		p, _ := s.AddPending(ctx(t), "alice", "c1", "secret")
		calls := map[string]error{}
		_, calls["AddPending"] = s.AddPending(ctx(t), "bob", "c1", "x")
		_, calls["Pending"] = s.Pending(ctx(t), "bob", "c1")
		calls["EditPending"] = s.EditPending(ctx(t), "bob", "c1", p.ID, "x")
		calls["CancelPending"] = s.CancelPending(ctx(t), "bob", "c1", p.ID)
		_, calls["TakePending"] = s.TakePending(ctx(t), "bob", "c1")
		calls["RestorePending"] = s.RestorePending(ctx(t), "bob", "c1", []string{p.ID})
		calls["ClaimTurn"] = s.ClaimTurn(ctx(t), "bob", "c1", "r1")
		calls["ReleaseTurn"] = s.ReleaseTurn(ctx(t), "bob", "c1", "r1", store.TurnDone)
		_, calls["Turn"] = s.Turn(ctx(t), "bob", "c1")
		for name, err := range calls {
			if !errors.Is(err, session.ErrNotFound) {
				t.Errorf("%s as another principal = %v, want ErrNotFound", name, err)
			}
		}
	})

	t.Run("exactly one claim wins", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		var wins, busy int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				switch err := s.ClaimTurn(ctx(t), "alice", "c1", string(rune('a'+i))); {
				case err == nil:
					atomic.AddInt32(&wins, 1)
				case errors.Is(err, store.ErrTurnRunning):
					atomic.AddInt32(&busy, 1)
				default:
					t.Errorf("ClaimTurn: %v", err)
				}
			}(i)
		}
		wg.Wait()
		if wins != 1 || busy != 7 {
			t.Fatalf("wins=%d busy=%d, want 1 and 7", wins, busy)
		}
	})

	t.Run("release frees the chat and records the outcome", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		if err := s.ClaimTurn(ctx(t), "alice", "c1", "r1"); err != nil {
			t.Fatal(err)
		}
		info, _ := s.Turn(ctx(t), "alice", "c1")
		if !info.Running || info.RunID != "r1" {
			t.Fatalf("running turn = %+v", info)
		}
		if err := s.ReleaseTurn(ctx(t), "alice", "c1", "other", store.TurnDone); !errors.Is(err, store.ErrNotClaimed) {
			t.Fatalf("release by another run = %v, want ErrNotClaimed", err)
		}
		if err := s.ReleaseTurn(ctx(t), "alice", "c1", "r1", store.TurnStopped); err != nil {
			t.Fatal(err)
		}
		info, _ = s.Turn(ctx(t), "alice", "c1")
		if info.Running || info.Last != store.TurnStopped {
			t.Fatalf("after release = %+v", info)
		}
		if err := s.ClaimTurn(ctx(t), "alice", "c1", "r2"); err != nil {
			t.Fatalf("claim after release: %v", err)
		}
	})

	t.Run("a turn that stops beating is reaped, one that keeps beating is not", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "stale", "alice")
		chat(t, s, "alive", "alice")
		if err := s.ClaimTurn(ctx(t), "alice", "stale", "r-stale"); err != nil {
			t.Fatal(err)
		}
		if err := s.ClaimTurn(ctx(t), "alice", "alive", "r-alive"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Millisecond)
		cutoff := time.Now()
		time.Sleep(40 * time.Millisecond)
		if err := s.TouchTurn(ctx(t), "alice", "alive", "r-alive"); err != nil {
			t.Fatal(err)
		}

		got, err := s.ReapTurns(ctx(t), cutoff)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ChatID != "stale" || got[0].Principal != "alice" || got[0].RunID != "r-stale" {
			t.Fatalf("reaped = %+v, want only the chat that stopped beating", got)
		}
		if info, _ := s.Turn(ctx(t), "alice", "stale"); info.Running || info.Last != store.TurnInterrupted {
			t.Fatalf("reaped turn = %+v, want not running and interrupted", info)
		}
		if info, _ := s.Turn(ctx(t), "alice", "alive"); !info.Running {
			t.Fatalf("a turn that kept beating was reaped: %+v", info)
		}
		// The run that was reaped can no longer beat or release, and the chat is free.
		if err := s.TouchTurn(ctx(t), "alice", "stale", "r-stale"); !errors.Is(err, store.ErrNotClaimed) {
			t.Fatalf("TouchTurn on a reaped run = %v, want ErrNotClaimed", err)
		}
		if err := s.ClaimTurn(ctx(t), "alice", "stale", "r-next"); err != nil {
			t.Fatalf("claim after reap: %v", err)
		}
		if again, _ := s.ReapTurns(ctx(t), cutoff); len(again) != 0 {
			t.Fatalf("a second reap found %+v", again)
		}
	})

	t.Run("a reaped turn gives back the messages its model request took but never confirmed", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		if err := s.ClaimTurn(ctx(t), "alice", "c1", "r1"); err != nil {
			t.Fatal(err)
		}
		lost, _ := s.AddPending(ctx(t), "alice", "c1", "taken, then the frontend died")
		kept, _ := s.AddPending(ctx(t), "alice", "c1", "taken and recorded")
		if _, err := s.TakePending(ctx(t), "alice", "c1"); err != nil {
			t.Fatal(err)
		}
		if err := s.ConfirmPending(ctx(t), "alice", "c1", []string{kept.ID}); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.ReapTurns(ctx(t), time.Now().Add(time.Hour)); len(got) != 1 {
			t.Fatalf("reaped = %+v, want the one turn", got)
		}
		pend, _ := s.Pending(ctx(t), "alice", "c1")
		if len(pend) != 1 || pend[0].ID != lost.ID || pend[0].Delivered {
			t.Fatalf("pending after the reap = %+v, want only the unconfirmed message, undelivered", pend)
		}
	})

	t.Run("touch is refused for another principal and for a run that does not hold the chat", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		_ = s.ClaimTurn(ctx(t), "alice", "c1", "r1")
		if err := s.TouchTurn(ctx(t), "bob", "c1", "r1"); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("TouchTurn as bob = %v, want ErrNotFound", err)
		}
		if err := s.TouchTurn(ctx(t), "alice", "c1", "other"); !errors.Is(err, store.ErrNotClaimed) {
			t.Fatalf("TouchTurn by another run = %v, want ErrNotClaimed", err)
		}
	})

	t.Run("racing takes never deliver a message twice", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		for i := 0; i < 20; i++ {
			_, _ = s.AddPending(ctx(t), "alice", "c1", string(rune('a'+i)))
		}
		var mu sync.Mutex
		seen := map[string]int{}
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := s.TakePending(ctx(t), "alice", "c1")
				if err != nil {
					t.Errorf("TakePending: %v", err)
					return
				}
				mu.Lock()
				for _, p := range got {
					seen[p.ID]++
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		if len(seen) != 20 {
			t.Fatalf("delivered %d distinct messages, want 20", len(seen))
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("message %s delivered %d times", id, n)
			}
		}
	})

	t.Run("deleting a chat removes its pending messages and claim", func(t *testing.T) {
		s := newStore(t)
		chat(t, s, "c1", "alice")
		_, _ = s.AddPending(ctx(t), "alice", "c1", "x")
		_ = s.ClaimTurn(ctx(t), "alice", "c1", "r1")
		if err := s.DeleteChat(ctx(t), "alice", "c1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pending(ctx(t), "alice", "c1"); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("Pending after delete = %v, want ErrNotFound", err)
		}
		chat(t, s, "c1", "alice") // the id can be reused, with a clean slate
		if got, _ := s.Pending(ctx(t), "alice", "c1"); len(got) != 0 {
			t.Fatalf("a recreated chat inherited %v", texts(got))
		}
		if err := s.ClaimTurn(ctx(t), "alice", "c1", "r2"); err != nil {
			t.Fatalf("a recreated chat inherited a claim: %v", err)
		}
	})
}

// Seed writes a small, known state. CheckSeed reads it back from another
// handle on the same storage (a reopened store), which proves persistence.
func Seed(t *testing.T, s store.Store) {
	t.Helper()
	if err := s.CreateChat(ctx(t), session.ChatRecord{ID: "seed", Principal: "alice", Title: "Kept", Created: t0, Updated: t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEntry(ctx(t), "seed", session.Entry{Role: "user", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPending(ctx(t), "alice", "seed", "waiting"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimTurn(ctx(t), "alice", "seed", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseTurn(ctx(t), "alice", "seed", "r1", store.TurnStopped); err != nil {
		t.Fatal(err)
	}
}

// CheckSeed fails the test unless s holds the state Seed wrote.
func CheckSeed(t *testing.T, s store.Store) {
	t.Helper()
	rec, err := s.GetChat(ctx(t), "alice", "seed")
	if err != nil || rec.Title != "Kept" {
		t.Fatalf("GetChat = %+v, %v", rec, err)
	}
	es, err := s.Entries(ctx(t), "seed", 0, 10)
	if err != nil || len(es) != 1 || es[0].Text != "hello" {
		t.Fatalf("Entries = %+v, %v", es, err)
	}
	ps, err := s.Pending(ctx(t), "alice", "seed")
	if err != nil || !same(texts(ps), []string{"waiting"}) {
		t.Fatalf("Pending = %v, %v", texts(ps), err)
	}
	info, err := s.Turn(ctx(t), "alice", "seed")
	if err != nil || info.Running || info.Last != store.TurnStopped {
		t.Fatalf("Turn = %+v, %v", info, err)
	}
}
