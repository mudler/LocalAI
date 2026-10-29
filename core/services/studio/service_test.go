package studio_test

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const alice = "alice"

func newService(h *studiotest.GateHarness) *studio.Service {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

func create(svc *studio.Service, user string) string {
	rec, err := svc.Create(context.Background(), user, studio.CreateInput{Model: "m"})
	Expect(err).NotTo(HaveOccurred())
	return rec.ID
}

func until(what string, f func() bool) {
	deadline := time.Now().Add(20 * time.Second)
	for !f() {
		Expect(time.Now().After(deadline)).To(BeFalse(), "timed out waiting for %s", what)
		time.Sleep(5 * time.Millisecond)
	}
}

func turn(svc *studio.Service, user, id string) store.TurnInfo {
	info, err := svc.Turn(context.Background(), user, id)
	Expect(err).NotTo(HaveOccurred())
	return info
}

func sent(svc *studio.Service, user, id, text string) studio.SendResult {
	r, err := svc.Send(context.Background(), user, id, text)
	Expect(err).NotTo(HaveOccurred())
	return r
}

func pendingTexts(svc *studio.Service, user, id string) []string {
	ps, err := svc.Pending(context.Background(), user, id)
	Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, p := range ps {
		out = append(out, p.Text)
	}
	return out
}

var _ = Describe("service", func() {
	It("a send starts a turn and it ends done", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		r := sent(svc, alice, id, "hi")
		Expect(r.Started).To(BeTrue(), "Send = %+v, want a started turn", r)
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		h.Gate <- struct{}{}
		until("the turn to end", func() bool { return !turn(svc, alice, id).Running })
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
		Expect(h.Sent()).To(Equal([]string{"hi"}), "harness got %v", h.Sent())
	})

	It("messages sent during a turn arrive as one ordered batch", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		for _, x := range []string{"one", "two", "three"} {
			r := sent(svc, alice, id, x)
			Expect(r.Started).To(BeFalse(), "Send during a turn = %+v, want pending", r)
			Expect(r.PendingID).NotTo(Equal(""), "Send during a turn = %+v, want pending", r)
		}
		got := pendingTexts(svc, alice, id)
		Expect(got).To(Equal([]string{"one", "two", "three"}), "pending = %v", got)
		h.Gate <- struct{}{} // the next model request takes the batch
		until("the turn to end", func() bool { return !turn(svc, alice, id).Running })
		d := h.Delivered()
		Expect(d).To(HaveLen(1), "delivered = %v, want one batch in order", d)
		Expect(d[0]).To(Equal([]string{"one", "two", "three"}), "delivered = %v, want one batch in order", d)
		Expect(pendingTexts(svc, alice, id)).To(BeEmpty())
	})

	It("pending messages can be edited and cancelled until delivered", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		a := sent(svc, alice, id, "keep")
		b := sent(svc, alice, id, "typo")
		c := sent(svc, alice, id, "drop")
		ctx := context.Background()
		Expect(svc.EditPending(ctx, alice, id, b.PendingID, "fixed")).To(Succeed())
		Expect(svc.CancelPending(ctx, alice, id, c.PendingID)).To(Succeed())
		h.Gate <- struct{}{}
		until("the turn to end", func() bool { return !turn(svc, alice, id).Running })
		d := h.Delivered()
		Expect(d).To(HaveLen(1), "delivered = %v", d)
		Expect(d[0]).To(Equal([]string{"keep", "fixed"}), "delivered = %v", d)
		Expect(svc.EditPending(ctx, alice, id, a.PendingID, "late")).To(MatchError(store.ErrDelivered))
	})

	// A message that arrives after the last model request of a turn but before the
	// turn ends still reaches the model: it starts the next turn.
	It("a message left over at the end starts the next turn", func() {
		h := studiotest.NewGateHarness()
		h.End = make(chan struct{}, 8) // the turn waits here after its last request
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "first")
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		h.Gate <- struct{}{} // the only request of the turn: nothing pending yet
		until("the harness to be past its request", func() bool { return h.Boundaries() == 1 })
		sent(svc, alice, id, "late")
		h.End <- struct{}{} // the turn ends; "late" was never taken
		until("the second turn to start", func() bool { return len(h.Sent()) == 2 })
		Expect(h.Sent()).To(Equal([]string{"first", "late"}), "turns = %v, want first then late", h.Sent())
		h.Gate <- struct{}{}
		h.End <- struct{}{}
		until("everything to settle", func() bool { return !turn(svc, alice, id).Running })
	})

	// A Stop is final until the user decides: waiting messages stay, and SendNow
	// is what starts them.
	It("stop leaves pending messages waiting and send now starts them", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		ctx := context.Background()
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		sent(svc, alice, id, "z1")
		sent(svc, alice, id, "z2")
		Expect(svc.Stop(ctx, alice, id)).To(Succeed())
		until("the turn to stop", func() bool { return !turn(svc, alice, id).Running })
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnStopped))
		time.Sleep(150 * time.Millisecond) // nothing may start by itself
		Expect(h.Sent()).To(HaveLen(1), "a turn started after Stop: %v", h.Sent())
		got := pendingTexts(svc, alice, id)
		Expect(got).To(Equal([]string{"z1", "z2"}), "pending after Stop = %v", got)
		Expect(svc.SendNow(ctx, alice, id)).To(Succeed())
		until("the turn to start", func() bool { return len(h.Sent()) == 2 })
		Expect(h.Sent()[1]).To(Equal("z1\n\nz2"), "SendNow started a turn with the messages joined in order")
	})

	It("a failed request puts the messages back and the turn fails", func() {
		h := studiotest.NewGateHarness()
		h.FailRequest = 1
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		sent(svc, alice, id, "keep me")
		h.Gate <- struct{}{} // the request takes "keep me", then fails
		until("the turn to fail", func() bool { return turn(svc, alice, id).Last == store.TurnFailed })
		got := pendingTexts(svc, alice, id)
		Expect(got).To(Equal([]string{"keep me"}), "pending after a failed request = %v, want the message back", got)
		time.Sleep(150 * time.Millisecond)
		Expect(h.Sent()).To(HaveLen(1), "a turn started after a failure: %v", h.Sent())
	})

	It("another user cannot see or use a conversation", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		ctx := context.Background()
		_, err := svc.Send(ctx, "bob", id, "hi")
		Expect(err).To(MatchError(session.ErrNotFound), "Send as bob = %v, want not found", err)
		_, err = svc.Pending(ctx, "bob", id)
		Expect(err).To(MatchError(session.ErrNotFound), "Pending as bob = %v", err)
		Expect(svc.Stop(ctx, "bob", id)).To(MatchError(session.ErrNotFound))
		_, _, err = svc.Events(ctx, "bob", id)
		Expect(err).To(MatchError(session.ErrNotFound), "Events as bob = %v", err)
	})

	It("events carry text and the delivered marker", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		events, cancel, err := svc.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		p := sent(svc, alice, id, "extra")
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
				Fail(fmt.Sprintf("timed out: text=%v delivered=%v", sawText, sawDelivered))
			}
		}
		Expect(sawText).To(BeTrue(), "text=%v pending_delivered=%v, want both", sawText, sawDelivered)
		Expect(sawDelivered).To(BeTrue(), "text=%v pending_delivered=%v, want both", sawText, sawDelivered)
	})

	It("delete removes a conversation for its owner only", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		ctx := context.Background()
		sent(svc, alice, id, "hi")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		h.Gate <- struct{}{}
		until("the turn to end", func() bool { return !turn(svc, alice, id).Running })

		Expect(svc.Delete(ctx, "bob", id)).To(MatchError(session.ErrNotFound))
		_, err := svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred(), "bob's failed delete removed the conversation")
		Expect(svc.Delete(ctx, alice, id)).To(Succeed())
		_, err = svc.Get(ctx, alice, id)
		Expect(err).To(MatchError(session.ErrNotFound), "Get after delete = %v, want not found", err)
		entries, err := svc.Messages(ctx, alice, id, 0, 10)
		Expect(err).To(MatchError(session.ErrNotFound), "Messages after delete = %v, %v", entries, err)
	})

	It("delete stops a running turn first", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		Expect(svc.Delete(context.Background(), alice, id)).To(Succeed())
		_, err := svc.Get(context.Background(), alice, id)
		Expect(err).To(MatchError(session.ErrNotFound), "Get after delete = %v", err)
	})

	// A new message must not overtake the ones that wait before it.
	It("a new message starts a turn behind the ones waiting before it", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		sent(svc, alice, id, "old")
		Expect(svc.Stop(context.Background(), alice, id)).To(Succeed())
		until("the turn to stop", func() bool { return !turn(svc, alice, id).Running })

		r := sent(svc, alice, id, "new")
		Expect(r.Started).To(BeTrue(), "Send = %+v, want a started turn", r)
		until("the second turn", func() bool { return len(h.Sent()) == 2 })
		got := h.Sent()[1]
		Expect(got).To(Equal("old\n\nnew"), "the turn started with %q, want the waiting message first", got)
		Expect(pendingTexts(svc, alice, id)).To(BeEmpty())
	})

	It("a chat is named after its first message", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		ctx := context.Background()
		id := create(svc, alice)
		rec, _ := svc.Get(ctx, alice, id)
		Expect(rec.Title).To(Equal(session.DefaultTitle), "a new chat is called %q", rec.Title)
		sent(svc, alice, id, "Plan the offsite budget\nfor the third quarter, with the venue costs we saw last year and a margin of ten percent")
		rec, err := svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Title).To(HavePrefix("Plan the offsite budget"), "the title is %q", rec.Title)
		Expect(rec.Title).NotTo(ContainSubstring("\n"), "the title is %q", rec.Title)
		Expect(utf8.RuneCountInString(rec.Title)).To(BeNumerically("<=", session.MaxTitleRunes), "the title is %q", rec.Title)
		// A title the user chose, or an earlier one, is not replaced by a later message.
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		sent(svc, alice, id, "A second message")
		again, _ := svc.Get(ctx, alice, id)
		Expect(again.Title).To(Equal(rec.Title), "the title changed to %q", again.Title)
		named, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", Title: "Mine"})
		Expect(err).NotTo(HaveOccurred())
		sent(svc, alice, named.ID, "something else entirely")
		got, _ := svc.Get(ctx, alice, named.ID)
		Expect(got.Title).To(Equal("Mine"), "a chosen title became %q", got.Title)
	})

	It("update renames a chat that is not running", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		ctx := context.Background()
		id := create(svc, alice)
		title := "  Budget\n 2027  "
		rec, err := svc.Update(ctx, alice, id, studio.UpdateInput{Title: &title})
		Expect(err).NotTo(HaveOccurred(), "Update = %+v, %v", rec, err)
		Expect(rec.Title).To(Equal("Budget 2027"), "Update = %+v, %v", rec, err)
		got, _ := svc.Get(ctx, alice, id)
		Expect(got.Title).To(Equal("Budget 2027"), "stored title %q", got.Title)
		for _, bad := range []string{"", "   ", strings.Repeat("x", session.MaxTitleRunes+1)} {
			_, err := svc.Update(ctx, alice, id, studio.UpdateInput{Title: &bad})
			Expect(err).To(MatchError(studio.ErrInvalidSettings), "Update(%q) = %v, want ErrInvalidSettings", bad, err)
		}
		mine := "Mine now"
		_, err = svc.Update(ctx, "bob", id, studio.UpdateInput{Title: &mine})
		Expect(err).To(MatchError(session.ErrNotFound), "another user renamed it: %v", err)
		sent(svc, alice, id, "go")
		until("the turn to start", func() bool { return turn(svc, alice, id).Running })
		running := "While running"
		_, err = svc.Update(ctx, alice, id, studio.UpdateInput{Title: &running})
		Expect(err).To(MatchError(studio.ErrChatBusy), "Update while a turn runs = %v, want ErrChatBusy", err)
	})

	It("turn end is announced after the turn is recorded", func() {
		h := studiotest.NewGateHarness()
		svc := newService(h)
		id := create(svc, alice)
		events, cancel, err := svc.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		sent(svc, alice, id, "hi")
		until("the harness to get the message", func() bool { return len(h.Sent()) >= 1 })
		h.Gate <- struct{}{}
		timeout := time.After(20 * time.Second)
		for {
			select {
			case e := <-events:
				if e.Kind != session.KindTurnEnd {
					continue
				}
				Expect(turn(svc, alice, id).Running).To(BeFalse(), "turn_end was announced while the chat was still recorded as running")
				return
			case <-timeout:
				Fail("timed out waiting for turn_end")
			}
		}
	})
})
