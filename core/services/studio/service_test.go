package studio_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

const alice = "alice"

func newService(t *testing.T, h *studiotest.GateHarness) *studio.Service {
	t.Helper()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

func create(t *testing.T, svc *studio.Service, user string) string {
	t.Helper()
	rec, err := svc.Create(context.Background(), user, studio.CreateInput{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

func until(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func turn(t *testing.T, svc *studio.Service, user, id string) store.TurnInfo {
	t.Helper()
	info, err := svc.Turn(context.Background(), user, id)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func sent(t *testing.T, svc *studio.Service, user, id, text string) studio.SendResult {
	t.Helper()
	r, err := svc.Send(context.Background(), user, id, text)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func pendingTexts(t *testing.T, svc *studio.Service, user, id string) []string {
	t.Helper()
	ps, err := svc.Pending(context.Background(), user, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range ps {
		out = append(out, p.Text)
	}
	return out
}

func eq(a, b []string) bool {
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

func TestASendStartsATurnAndItEndsDone(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	if r := sent(t, svc, alice, id, "hi"); !r.Started {
		t.Fatalf("Send = %+v, want a started turn", r)
	}
	until(t, "the turn to start", func() bool { return turn(t, svc, alice, id).Running })
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	h.Gate <- struct{}{}
	until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })
	if got := turn(t, svc, alice, id).Last; got != store.TurnDone {
		t.Fatalf("turn ended %q, want done", got)
	}
	if !eq(h.Sent(), []string{"hi"}) {
		t.Fatalf("harness got %v", h.Sent())
	}
}

func TestMessagesSentDuringATurnArriveAsOneOrderedBatch(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "start")
	until(t, "the turn to start", func() bool { return turn(t, svc, alice, id).Running })
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	for _, x := range []string{"one", "two", "three"} {
		if r := sent(t, svc, alice, id, x); r.Started || r.PendingID == "" {
			t.Fatalf("Send during a turn = %+v, want pending", r)
		}
	}
	if got := pendingTexts(t, svc, alice, id); !eq(got, []string{"one", "two", "three"}) {
		t.Fatalf("pending = %v", got)
	}
	h.Gate <- struct{}{} // the next model request takes the batch
	until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })
	if d := h.Delivered(); len(d) != 1 || !eq(d[0], []string{"one", "two", "three"}) {
		t.Fatalf("delivered = %v, want one batch in order", d)
	}
	if got := pendingTexts(t, svc, alice, id); len(got) != 0 {
		t.Fatalf("pending after delivery = %v", got)
	}
}

func TestPendingMessagesCanBeEditedAndCancelledUntilDelivered(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "start")
	until(t, "the turn to start", func() bool { return turn(t, svc, alice, id).Running })
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	a := sent(t, svc, alice, id, "keep")
	b := sent(t, svc, alice, id, "typo")
	c := sent(t, svc, alice, id, "drop")
	ctx := context.Background()
	if err := svc.EditPending(ctx, alice, id, b.PendingID, "fixed"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelPending(ctx, alice, id, c.PendingID); err != nil {
		t.Fatal(err)
	}
	h.Gate <- struct{}{}
	until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })
	if d := h.Delivered(); len(d) != 1 || !eq(d[0], []string{"keep", "fixed"}) {
		t.Fatalf("delivered = %v", d)
	}
	if err := svc.EditPending(ctx, alice, id, a.PendingID, "late"); !errors.Is(err, store.ErrDelivered) {
		t.Fatalf("edit after delivery = %v, want ErrDelivered", err)
	}
}

// A message that arrives after the last model request of a turn but before the
// turn ends still reaches the model: it starts the next turn.
func TestAMessageLeftOverAtTheEndStartsTheNextTurn(t *testing.T) {
	h := studiotest.NewGateHarness()
	h.End = make(chan struct{}, 8) // the turn waits here after its last request
	svc := newService(t, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "first")
	until(t, "the turn to start", func() bool { return turn(t, svc, alice, id).Running })
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	h.Gate <- struct{}{} // the only request of the turn: nothing pending yet
	until(t, "the harness to be past its request", func() bool { return h.Boundaries() == 1 })
	sent(t, svc, alice, id, "late")
	h.End <- struct{}{} // the turn ends; "late" was never taken
	until(t, "the second turn to start", func() bool { return len(h.Sent()) == 2 })
	if !eq(h.Sent(), []string{"first", "late"}) {
		t.Fatalf("turns = %v, want first then late", h.Sent())
	}
	h.Gate <- struct{}{}
	h.End <- struct{}{}
	until(t, "everything to settle", func() bool { return !turn(t, svc, alice, id).Running })
}

// A Stop is final until the user decides: waiting messages stay, and SendNow
// is what starts them.
func TestStopLeavesPendingMessagesWaitingAndSendNowStartsThem(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	ctx := context.Background()
	sent(t, svc, alice, id, "start")
	until(t, "the turn to start", func() bool { return turn(t, svc, alice, id).Running })
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	sent(t, svc, alice, id, "z1")
	sent(t, svc, alice, id, "z2")
	if err := svc.Stop(ctx, alice, id); err != nil {
		t.Fatal(err)
	}
	until(t, "the turn to stop", func() bool { return !turn(t, svc, alice, id).Running })
	if got := turn(t, svc, alice, id).Last; got != store.TurnStopped {
		t.Fatalf("turn ended %q, want stopped", got)
	}
	time.Sleep(150 * time.Millisecond) // nothing may start by itself
	if len(h.Sent()) != 1 {
		t.Fatalf("a turn started after Stop: %v", h.Sent())
	}
	if got := pendingTexts(t, svc, alice, id); !eq(got, []string{"z1", "z2"}) {
		t.Fatalf("pending after Stop = %v", got)
	}
	if err := svc.SendNow(ctx, alice, id); err != nil {
		t.Fatal(err)
	}
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 2 })
	if got := h.Sent()[1]; got != "z1\n\nz2" {
		t.Fatalf("SendNow started a turn with %q, want the messages joined in order", got)
	}
}

func TestAFailedRequestPutsTheMessagesBackAndTheTurnFails(t *testing.T) {
	h := studiotest.NewGateHarness()
	h.FailRequest = 1
	svc := newService(t, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "start")
	until(t, "the turn to start", func() bool { return turn(t, svc, alice, id).Running })
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	sent(t, svc, alice, id, "keep me")
	h.Gate <- struct{}{} // the request takes "keep me", then fails
	until(t, "the turn to fail", func() bool { return turn(t, svc, alice, id).Last == store.TurnFailed })
	if got := pendingTexts(t, svc, alice, id); !eq(got, []string{"keep me"}) {
		t.Fatalf("pending after a failed request = %v, want the message back", got)
	}
	time.Sleep(150 * time.Millisecond)
	if len(h.Sent()) != 1 {
		t.Fatalf("a turn started after a failure: %v", h.Sent())
	}
}

func TestAnotherUserCannotSeeOrUseAConversation(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	ctx := context.Background()
	if _, err := svc.Send(ctx, "bob", id, "hi"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Send as bob = %v, want not found", err)
	}
	if _, err := svc.Pending(ctx, "bob", id); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Pending as bob = %v", err)
	}
	if err := svc.Stop(ctx, "bob", id); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Stop as bob = %v", err)
	}
	if _, _, err := svc.Events(ctx, "bob", id); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Events as bob = %v", err)
	}
}

func TestEventsCarryTextAndTheDeliveredMarker(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	events, cancel, err := svc.Events(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	sent(t, svc, alice, id, "start")
	until(t, "the turn to start", func() bool { return turn(t, svc, alice, id).Running })
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	p := sent(t, svc, alice, id, "extra")
	h.Gate <- struct{}{}
	var sawText, sawDelivered, sawEnd bool
	timeout := time.After(20 * time.Second)
	for !sawEnd {
		select {
		case e := <-events:
			switch e.Kind {
			case session.KindText:
				sawText = true
			case session.KindPendingDelivered:
				sawDelivered = len(e.IDs) == 1 && e.IDs[0] == p.PendingID
			case session.KindTurnEnd:
				sawEnd = true
			}
		case <-timeout:
			t.Fatalf("timed out: text=%v delivered=%v", sawText, sawDelivered)
		}
	}
	if !sawText || !sawDelivered {
		t.Fatalf("text=%v pending_delivered=%v, want both", sawText, sawDelivered)
	}
}

func TestDeleteRemovesAConversationForItsOwnerOnly(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	ctx := context.Background()
	sent(t, svc, alice, id, "hi")
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	h.Gate <- struct{}{}
	until(t, "the turn to end", func() bool { return !turn(t, svc, alice, id).Running })

	if err := svc.Delete(ctx, "bob", id); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Delete as bob = %v, want not found", err)
	}
	if _, err := svc.Get(ctx, alice, id); err != nil {
		t.Fatalf("bob's failed delete removed the conversation: %v", err)
	}
	if err := svc.Delete(ctx, alice, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, alice, id); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Get after delete = %v, want not found", err)
	}
	if entries, err := svc.Messages(ctx, alice, id, 0, 10); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Messages after delete = %v, %v", entries, err)
	}
}

func TestDeleteStopsARunningTurnFirst(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	if err := svc.Delete(context.Background(), alice, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), alice, id); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Get after delete = %v", err)
	}
}

// A new message must not overtake the ones that wait before it.
func TestANewMessageStartsATurnBehindTheOnesWaitingBeforeIt(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newService(t, h)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "start")
	until(t, "the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
	sent(t, svc, alice, id, "old")
	if err := svc.Stop(context.Background(), alice, id); err != nil {
		t.Fatal(err)
	}
	until(t, "the turn to stop", func() bool { return !turn(t, svc, alice, id).Running })

	if r := sent(t, svc, alice, id, "new"); !r.Started {
		t.Fatalf("Send = %+v, want a started turn", r)
	}
	until(t, "the second turn", func() bool { return len(h.Sent()) == 2 })
	if got := h.Sent()[1]; got != "old\n\nnew" {
		t.Fatalf("the turn started with %q, want the waiting message first", got)
	}
	if got := pendingTexts(t, svc, alice, id); len(got) != 0 {
		t.Fatalf("pending = %v, want none: the waiting message went with the turn", got)
	}
}
