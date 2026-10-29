package studio_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// frontends builds two services that share one store and one message bus, as
// two replicas of LocalAI in a cluster do. Each has its own harness, so a test
// can see which frontend ran a turn.
type frontends struct {
	a, b   *studio.Service
	ha, hb *studiotest.GateHarness
	st     store.Store
}

func newFrontends(wrap func(store.Store) store.Store) *frontends {
	var st store.Store
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	if wrap != nil {
		st = wrap(st)
	}
	net := testutil.NewFakeBus()
	f := &frontends{ha: studiotest.NewGateHarness(), hb: studiotest.NewGateHarness(), st: st}
	mk := func(h *studiotest.GateHarness) *studio.Service {
		svc, err := studio.New(studio.Deps{
			Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h,
			Bus: studio.NewBroadcastBus(net, nil), HeartbeatEvery: 10 * time.Millisecond,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		return svc
	}
	f.a, f.b = mk(f.ha), mk(f.hb)
	return f
}

var _ = Describe("cluster", func() {
	It("the events of a turn reach a client on another frontend", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		events, cancel, err := f.b.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		sent(f.a, alice, id, "go")
		until("frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
		f.ha.Gate <- struct{}{}
		var sawText, sawEnd bool
		timeout := time.After(10 * time.Second)
		for !sawEnd {
			select {
			case e := <-events:
				sawText = sawText || e.Kind == session.KindText
				sawEnd = e.Kind == session.KindTurnEnd
			case <-timeout:
				Fail(fmt.Sprintf("timed out: text=%v", sawText))
			}
		}
		Expect(sawText).To(BeTrue(), "text=%v, frontend B ran %v: only A may run the turn", sawText, f.hb.Sent())
		Expect(f.hb.Sent()).To(BeEmpty(), "text=%v, frontend B ran %v: only A may run the turn", sawText, f.hb.Sent())
	})

	It("another frontend can stop the turn", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		sent(f.a, alice, id, "go")
		until("frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
		Expect(f.b.Stop(context.Background(), alice, id)).To(Succeed())
		until("the turn to stop", func() bool { return turn(f.b, alice, id).Last == store.TurnStopped })
	})

	It("messages sent on another frontend reach the running turn", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		sent(f.a, alice, id, "go")
		until("frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
		r := sent(f.b, alice, id, "from B")
		Expect(r.Started).To(BeFalse(), "Send on B during A's turn = %+v, want pending", r)
		Expect(r.PendingID).NotTo(Equal(""), "Send on B during A's turn = %+v, want pending", r)
		f.ha.Gate <- struct{}{} // A's next model request takes the batch
		until("the turn to end", func() bool { return !turn(f.a, alice, id).Running })
		d := f.ha.Delivered()
		Expect(d).To(HaveLen(1), "A delivered %v, want B's message", d)
		Expect(d[0]).To(Equal([]string{"from B"}), "A delivered %v, want B's message", d)
	})

	It("only one frontend starts a turn", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		var started int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				svc := f.a
				if i%2 == 1 {
					svc = f.b
				}
				r, err := svc.Send(context.Background(), alice, id, "m")
				Expect(err).NotTo(HaveOccurred())
				if r.Started {
					atomic.AddInt32(&started, 1)
				}
			}(i)
		}
		wg.Wait()
		Expect(started).To(BeNumerically("==", 1), "%d turns started, want exactly one", started)
		until("the one turn to reach its harness", func() bool { return len(f.ha.Sent())+len(f.hb.Sent()) >= 1 })
		time.Sleep(100 * time.Millisecond) // a second turn would show by now
		Expect(len(f.ha.Sent()) + len(f.hb.Sent())).To(BeNumerically("==", 1))
	})

	// A frontend that dies leaves its claim behind. Reaping frees the chat, tells
	// whoever watches, and the next message runs.
	It("another frontend reaps a turn whose frontend died", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		events, cancel, err := f.b.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		// A claim that no live frontend holds, as a dead frontend leaves.
		Expect(f.st.ClaimTurn(context.Background(), alice, id, "ghost")).To(Succeed())
		time.Sleep(60 * time.Millisecond)
		n, err := f.b.ReapStale(context.Background(), 30*time.Millisecond)
		Expect(err).NotTo(HaveOccurred(), "ReapStale = %d, %v, want 1", n, err)
		Expect(n).To(BeNumerically("==", 1), "ReapStale = %d, %v, want 1", n, err)
		info := turn(f.b, alice, id)
		Expect(info.Running).To(BeFalse(), "turn after reap = %+v", info)
		Expect(info.Last).To(Equal(store.TurnInterrupted), "turn after reap = %+v", info)
		var sawEnd bool
		timeout := time.After(5 * time.Second)
		for !sawEnd {
			select {
			case e := <-events:
				sawEnd = e.Kind == session.KindTurnEnd
			case <-timeout:
				Fail("watchers were not told that the turn ended")
			}
		}
		r := sent(f.b, alice, id, "again")
		Expect(r.Started).To(BeTrue(), "Send after the reap = %+v, want a started turn", r)
	})

	It("a running turn keeps beating so it is not reaped", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		sent(f.a, alice, id, "go")
		until("frontend A to run the turn", func() bool { return len(f.ha.Sent()) == 1 })
		time.Sleep(250 * time.Millisecond) // many beats, far more than the cutoff
		n, err := f.b.ReapStale(context.Background(), 100*time.Millisecond)
		Expect(err).NotTo(HaveOccurred(), "ReapStale = %d, %v: a live turn was reaped", n, err)
		Expect(n).To(BeNumerically("==", 0), "ReapStale = %d, %v: a live turn was reaped", n, err)
		Expect(turn(f.a, alice, id).Running).To(BeTrue(), "the live turn is no longer running")
		f.ha.Gate <- struct{}{}
		until("the turn to end", func() bool { return !turn(f.a, alice, id).Running })
		Expect(turn(f.a, alice, id).Last).To(Equal(store.TurnDone), "the turn should end normally")
	})
})

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
var _ = Describe("cluster", func() {
	It("a message queued just after the turn ended starts the next turn", func() {
		var racy *racyStore
		f := newFrontends(func(s store.Store) store.Store {
			racy = &racyStore{Store: s}
			racy.release = func(ctx context.Context, p, c string) {
				info, _ := s.Turn(ctx, p, c)
				_ = s.ReleaseTurn(ctx, p, c, info.RunID, store.TurnDone)
			}
			return racy
		})
		id := create(f.b, alice)
		Expect(f.st.ClaimTurn(context.Background(), alice, id, "elsewhere")).To(Succeed())
		r := sent(f.b, alice, id, "late message")
		until("a turn to start for the late message", func() bool { return len(f.hb.Sent()) == 1 })
		Expect(f.hb.Sent()[0]).To(Equal("late message"))
		Expect(r.PendingID).NotTo(Equal(""), "Send = %+v", r)
	})

	It("a turn on one frontend is seen by the next turn on another", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		runTurn := func(svc *studio.Service, h *studiotest.GateHarness, text string) {
			sent(svc, alice, id, text)
			until("the turn to start", func() bool { return len(h.Sent()) > 0 && h.Sent()[len(h.Sent())-1] == text })
			h.Gate <- struct{}{}
			until("the turn to end", func() bool { return !turn(svc, alice, id).Running })
		}
		runTurn(f.a, f.ha, "one")
		runTurn(f.b, f.hb, "two")
		runTurn(f.a, f.ha, "three")

		rec, err := f.st.GetChat(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Turns).To(BeNumerically("==", 3), "the chat has %d turns after three, one on each of A, B, A: A kept a stale count", rec.Turns)
		entries, err := f.st.Entries(context.Background(), id, 0, 100)
		Expect(err).NotTo(HaveOccurred())
		var turns []int
		for _, e := range entries {
			if e.Role == session.RoleUser {
				turns = append(turns, e.Turn)
			}
		}
		Expect(turns).To(HaveLen(3), "user messages are in turns %v, want 1 2 3", turns)
		Expect(turns[0]).To(BeNumerically("==", 1), "user messages are in turns %v, want 1 2 3", turns)
		Expect(turns[1]).To(BeNumerically("==", 2), "user messages are in turns %v, want 1 2 3", turns)
		Expect(turns[2]).To(BeNumerically("==", 3), "user messages are in turns %v, want 1 2 3", turns)
	})
})

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

var _ = Describe("cluster", func() {
	It("a message queued while a follow up finds nothing still starts", func() {
		var hook *afterTake
		f := newFrontends(func(s store.Store) store.Store { hook = &afterTake{Store: s}; return hook })
		id := create(f.a, alice)
		sent(f.a, alice, id, "first")
		until("the turn to start", func() bool { return len(f.ha.Sent()) == 1 })
		hook.mu.Lock()
		hook.hook = func() { // B sends while A's claim is still held, before A lets go
			_, err := f.b.Send(context.Background(), alice, id, "late")
			Expect(err).NotTo(HaveOccurred())
		}
		hook.mu.Unlock()
		f.ha.Gate <- struct{}{}
		until("the late message to start a turn", func() bool {
			for _, h := range []*studiotest.GateHarness{f.ha, f.hb} {
				for _, s := range h.Sent() {
					if s == "late" {
						return true
					}
				}
			}
			return false
		})
	})
})

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

var _ = Describe("cluster", func() {
	It("a turn whose claim was reaped stops instead of running on", func() {
		var lost *touchLost
		f := newFrontends(func(s store.Store) store.Store { lost = &touchLost{Store: s}; return lost })
		id := create(f.a, alice)
		events, cancel, err := f.a.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		sent(f.a, alice, id, "go")
		until("the turn to start", func() bool { return len(f.ha.Sent()) == 1 })
		lost.lost.Store(true)
		timeout := time.After(10 * time.Second)
		for {
			select {
			case e := <-events:
				if e.Kind == session.KindTurnEnd {
					return
				}
			case <-timeout:
				Fail("the turn kept running after its claim was lost")
			}
		}
	})

	It("a message taken by a dead frontend is given back when its turn is reaped", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		f.ha.FailRequest = 0
		sent(f.a, alice, id, "first")
		until("the turn to start", func() bool { return len(f.ha.Sent()) == 1 })
		sent(f.b, alice, id, "steer")
		// A takes the steer at its next request, then the frontend dies: the
		// request neither settles nor records it.
		taken, err := f.st.TakePending(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred(), "TakePending = %v, %v", taken, err)
		Expect(taken).To(HaveLen(1), "TakePending = %v, %v", taken, err)
		_, err = f.b.ReapStale(context.Background(), -time.Hour)
		Expect(err).NotTo(HaveOccurred())
		pend, err := f.b.Pending(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred(), "pending after the reap = %+v, %v, want the steer back", pend, err)
		Expect(pend).To(HaveLen(1), "pending after the reap = %+v, %v, want the steer back", pend, err)
		Expect(pend[0].Text).To(Equal("steer"), "pending after the reap = %+v, %v, want the steer back", pend, err)
	})
})
