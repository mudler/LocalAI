package studio_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// frontends builds two services that share one store and one message bus, as
// two replicas of LocalAI in a cluster do. Each has its own harness, so a test
// can see which frontend ran a turn.
type frontends struct {
	a, b   *studio.Service
	ha, hb *studiotest.GateHarness
	st     store.Store
}

func newFrontends(t *testing.T, wrap func(store.Store) store.Store) *frontends {
	t.Helper()
	var st store.Store
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	if wrap != nil {
		st = wrap(st)
	}
	net := testutil.NewFakeBus()
	f := &frontends{ha: studiotest.NewGateHarness(), hb: studiotest.NewGateHarness(), st: st}
	mk := func(h *studiotest.GateHarness) *studio.Service {
		svc, err := studio.New(studio.Deps{
			Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h,
			Bus: studio.NewBroadcastBus(net, nil), HeartbeatEvery: 10 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = svc.Close(context.Background()) })
		return svc
	}
	f.a, f.b = mk(f.ha), mk(f.hb)
	return f
}

func TestTheEventsOfATurnReachAClientOnAnotherFrontend(t *testing.T) {
	f := newFrontends(t, nil)
	id := create(t, f.a, alice)
	events, cancel, err := f.b.Events(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	sent(t, f.a, alice, id, "go")
	until(t, "frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
	f.ha.Gate <- struct{}{}
	var sawText, sawEnd bool
	timeout := time.After(10 * time.Second)
	for !sawEnd {
		select {
		case e := <-events:
			sawText = sawText || e.Kind == session.KindText
			sawEnd = e.Kind == session.KindTurnEnd
		case <-timeout:
			t.Fatalf("timed out: text=%v", sawText)
		}
	}
	if !sawText || len(f.hb.Sent()) != 0 {
		t.Fatalf("text=%v, frontend B ran %v: only A may run the turn", sawText, f.hb.Sent())
	}
}

func TestAnotherFrontendCanStopTheTurn(t *testing.T) {
	f := newFrontends(t, nil)
	id := create(t, f.a, alice)
	sent(t, f.a, alice, id, "go")
	until(t, "frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
	if err := f.b.Stop(context.Background(), alice, id); err != nil {
		t.Fatal(err)
	}
	until(t, "the turn to stop", func() bool { return turn(t, f.b, alice, id).Last == store.TurnStopped })
}

func TestMessagesSentOnAnotherFrontendReachTheRunningTurn(t *testing.T) {
	f := newFrontends(t, nil)
	id := create(t, f.a, alice)
	sent(t, f.a, alice, id, "go")
	until(t, "frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
	r := sent(t, f.b, alice, id, "from B")
	if r.Started || r.PendingID == "" {
		t.Fatalf("Send on B during A's turn = %+v, want pending", r)
	}
	f.ha.Gate <- struct{}{} // A's next model request takes the batch
	until(t, "the turn to end", func() bool { return !turn(t, f.a, alice, id).Running })
	if d := f.ha.Delivered(); len(d) != 1 || !eq(d[0], []string{"from B"}) {
		t.Fatalf("A delivered %v, want B's message", d)
	}
}

func TestOnlyOneFrontendStartsATurn(t *testing.T) {
	f := newFrontends(t, nil)
	id := create(t, f.a, alice)
	var started int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := f.a
			if i%2 == 1 {
				svc = f.b
			}
			r, err := svc.Send(context.Background(), alice, id, "m")
			if err != nil {
				t.Error(err)
				return
			}
			if r.Started {
				atomic.AddInt32(&started, 1)
			}
		}(i)
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("%d turns started, want exactly one", started)
	}
	until(t, "the one turn to reach its harness", func() bool { return len(f.ha.Sent())+len(f.hb.Sent()) >= 1 })
	time.Sleep(100 * time.Millisecond) // a second turn would show by now
	if n := len(f.ha.Sent()) + len(f.hb.Sent()); n != 1 {
		t.Fatalf("the harnesses ran %d turns, want 1", n)
	}
}

// A frontend that dies leaves its claim behind. Reaping frees the chat, tells
// whoever watches, and the next message runs.
func TestAnotherFrontendReapsATurnWhoseFrontendDied(t *testing.T) {
	f := newFrontends(t, nil)
	id := create(t, f.a, alice)
	events, cancel, err := f.b.Events(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	// A claim that no live frontend holds, as a dead frontend leaves.
	if err := f.st.ClaimTurn(context.Background(), alice, id, "ghost"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	n, err := f.b.ReapStale(context.Background(), 30*time.Millisecond)
	if err != nil || n != 1 {
		t.Fatalf("ReapStale = %d, %v, want 1", n, err)
	}
	if info := turn(t, f.b, alice, id); info.Running || info.Last != store.TurnInterrupted {
		t.Fatalf("turn after reap = %+v", info)
	}
	var sawEnd bool
	timeout := time.After(5 * time.Second)
	for !sawEnd {
		select {
		case e := <-events:
			sawEnd = e.Kind == session.KindTurnEnd
		case <-timeout:
			t.Fatal("watchers were not told that the turn ended")
		}
	}
	if r := sent(t, f.b, alice, id, "again"); !r.Started {
		t.Fatalf("Send after the reap = %+v, want a started turn", r)
	}
}

func TestARunningTurnKeepsBeatingSoItIsNotReaped(t *testing.T) {
	f := newFrontends(t, nil)
	id := create(t, f.a, alice)
	sent(t, f.a, alice, id, "go")
	until(t, "frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
	time.Sleep(250 * time.Millisecond) // many beats, far more than the cutoff
	n, err := f.b.ReapStale(context.Background(), 100*time.Millisecond)
	if err != nil || n != 0 {
		t.Fatalf("ReapStale = %d, %v: a live turn was reaped", n, err)
	}
	if !turn(t, f.a, alice, id).Running {
		t.Fatal("the live turn is no longer running")
	}
	f.ha.Gate <- struct{}{}
	until(t, "the turn to end", func() bool { return !turn(t, f.a, alice, id).Running })
	if turn(t, f.a, alice, id).Last != store.TurnDone {
		t.Fatal("the turn should end normally")
	}
}

// racyStore ends the running turn the moment a message is queued behind it, as
// another frontend finishing its turn just then would.
type racyStore struct {
	store.Store
	once    sync.Once
	release func(ctx context.Context, principal, chatID string)
}

func (r *racyStore) AddPending(ctx context.Context, principal, chatID, text string) (store.Pending, error) {
	r.once.Do(func() { r.release(ctx, principal, chatID) })
	return r.Store.AddPending(ctx, principal, chatID, text)
}

// A message that is queued just after the turn ended elsewhere must not wait
// for a turn that will never come.
func TestAMessageQueuedJustAfterTheTurnEndedStartsTheNextTurn(t *testing.T) {
	var racy *racyStore
	f := newFrontends(t, func(s store.Store) store.Store {
		racy = &racyStore{Store: s}
		racy.release = func(ctx context.Context, p, c string) {
			info, _ := s.Turn(ctx, p, c)
			_ = s.ReleaseTurn(ctx, p, c, info.RunID, store.TurnDone)
		}
		return racy
	})
	id := create(t, f.b, alice)
	if err := f.st.ClaimTurn(context.Background(), alice, id, "elsewhere"); err != nil {
		t.Fatal(err)
	}
	r := sent(t, f.b, alice, id, "late message")
	until(t, "a turn to start for the late message", func() bool { return len(f.hb.Sent()) == 1 })
	if got := f.hb.Sent()[0]; got != "late message" {
		t.Fatalf("the turn got %q", got)
	}
	if r.PendingID == "" {
		t.Fatalf("Send = %+v", r)
	}
}

func TestATurnOnOneFrontendIsSeenByTheNextTurnOnAnother(t *testing.T) {
	f := newFrontends(t, nil)
	id := create(t, f.a, alice)
	runTurn := func(svc *studio.Service, h *studiotest.GateHarness, text string) {
		sent(t, svc, alice, id, text)
		until(t, "the turn to start", func() bool { return len(h.Sent()) > 0 && h.Sent()[len(h.Sent())-1] == text })
		h.Gate <- struct{}{}
		until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })
	}
	runTurn(f.a, f.ha, "one")
	runTurn(f.b, f.hb, "two")
	runTurn(f.a, f.ha, "three")

	rec, err := f.st.GetChat(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Turns != 3 {
		t.Fatalf("the chat has %d turns after three, one on each of A, B, A: A kept a stale count", rec.Turns)
	}
	entries, err := f.st.Entries(context.Background(), id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var turns []int
	for _, e := range entries {
		if e.Role == session.RoleUser {
			turns = append(turns, e.Turn)
		}
	}
	if len(turns) != 3 || turns[0] != 1 || turns[1] != 2 || turns[2] != 3 {
		t.Fatalf("user messages are in turns %v, want 1 2 3", turns)
	}
}

// afterTake runs a hook once, right after the second TakePending that finds
// nothing. The first is the model request of a turn; the second is the
// follow-up that claimed the chat, found no message and is about to let go.
type afterTake struct {
	store.Store
	mu    sync.Mutex
	hook  func()
	empty int
}

func (a *afterTake) TakePending(ctx context.Context, p, c string) ([]store.Pending, error) {
	out, err := a.Store.TakePending(ctx, p, c)
	a.mu.Lock()
	var h func()
	if len(out) == 0 && a.hook != nil {
		if a.empty++; a.empty == 2 {
			h, a.hook = a.hook, nil
		}
	}
	a.mu.Unlock()
	if h != nil {
		h()
	}
	return out, err
}

func TestAMessageQueuedWhileAFollowUpFindsNothingStillStarts(t *testing.T) {
	var hook *afterTake
	f := newFrontends(t, func(s store.Store) store.Store { hook = &afterTake{Store: s}; return hook })
	id := create(t, f.a, alice)
	sent(t, f.a, alice, id, "first")
	until(t, "the turn to start", func() bool { return len(f.ha.Sent()) == 1 })
	hook.mu.Lock()
	hook.hook = func() { // B sends while A's claim is still held, before A lets go
		if _, err := f.b.Send(context.Background(), alice, id, "late"); err != nil {
			t.Error(err)
		}
	}
	hook.mu.Unlock()
	f.ha.Gate <- struct{}{}
	until(t, "the late message to start a turn", func() bool {
		for _, h := range []*studiotest.GateHarness{f.ha, f.hb} {
			for _, s := range h.Sent() {
				if s == "late" {
					return true
				}
			}
		}
		return false
	})
}

// touchLost answers every heartbeat as the store does for a turn that was
// reaped: the run no longer holds the chat.
type touchLost struct {
	store.Store
	lost atomic.Bool
}

func (a *touchLost) TouchTurn(ctx context.Context, p, c, run string) error {
	if a.lost.Load() {
		return store.ErrNotClaimed
	}
	return a.Store.TouchTurn(ctx, p, c, run)
}

func TestATurnWhoseClaimWasReapedStopsInsteadOfRunningOn(t *testing.T) {
	var lost *touchLost
	f := newFrontends(t, func(s store.Store) store.Store { lost = &touchLost{Store: s}; return lost })
	id := create(t, f.a, alice)
	events, cancel, err := f.a.Events(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	sent(t, f.a, alice, id, "go")
	until(t, "the turn to start", func() bool { return len(f.ha.Sent()) == 1 })
	lost.lost.Store(true)
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind == session.KindTurnEnd {
				return
			}
		case <-timeout:
			t.Fatal("the turn kept running after its claim was lost")
		}
	}
}
