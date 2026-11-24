package studio_test

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// lossyBus drops the events of one kind on their way to the subscribers, like
// a frontend that lost some messages of the bus.
type lossyBus struct {
	studio.Bus
	kind atomic.Value // session.EventKind
}

func (b *lossyBus) Publish(key string, ev session.Event) {
	if k, _ := b.kind.Load().(session.EventKind); k != "" && k == ev.Kind {
		return
	}
	b.Bus.Publish(key, ev)
}

func heldService(bus studio.Bus) (*studio.Service, *studiotest.GateHarness) {
	h := studiotest.NewGateHarness()
	h.End = make(chan struct{}, 1)
	h.Gate <- struct{}{}
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Bus: bus})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc, h
}

// drain reads events until one of kind stop arrives.
func drain(ch <-chan session.Event, stop session.EventKind) []session.Event {
	var out []session.Event
	for {
		e := recv(ch)
		out = append(out, e)
		if e.Kind == stop {
			return out
		}
	}
}

var _ = Describe("chat event replay", func() {
	ctx := context.Background()

	It("numbers the events of a turn and gives a reconnecting client what it missed", func() {
		svc, h := heldService(nil)
		id := create(svc, alice)
		live, cancelLive, err := svc.Events(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancelLive()
		sent(svc, alice, id, "hello")
		first := recv(live)
		for first.Kind != session.KindText {
			first = recv(live)
		}
		Expect(first.ID).To(MatchRegexp(`^[0-9a-f]+\.[0-9]+$`))

		// The client goes away; the turn goes on.
		h.End <- struct{}{}
		idle(svc, alice, id)

		got, cancel, err := svc.EventsAfter(ctx, alice, id, first.ID)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		missed := drain(got, session.KindTurnEnd)
		var kinds []session.EventKind
		for _, e := range missed {
			Expect(e.ID).ToNot(BeEmpty())
			kinds = append(kinds, e.Kind)
		}
		Expect(kinds).To(ContainElements(session.KindText, session.KindCheckpoint, session.KindTurnEnd))
		Expect(kinds).ToNot(ContainElement(session.KindResync))
		for _, e := range missed {
			Expect(e.ID).ToNot(Equal(first.ID), "the event the client had was sent again")
		}
		Expect(missed[0].Text).To(Equal("done"))
	})

	It("sends only the live events when the id is the newest", func() {
		svc, h := heldService(nil)
		id := create(svc, alice)
		live, cancelLive, _ := svc.Events(ctx, alice, id)
		defer cancelLive()
		sent(svc, alice, id, "hello")
		var head session.Event
		for head.Kind != session.KindText {
			head = recv(live)
		}
		got, cancel, err := svc.EventsAfter(ctx, alice, id, head.ID)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		none(got)
		h.End <- struct{}{}
		Expect(recv(got).Text).To(Equal("done"))
	})

	It("starts with a resync for an id of another run, a bad id, or a chat with no ring", func() {
		svc, h := heldService(nil)
		id := create(svc, alice)
		for _, after := range []string{"nope", "other.3", "x.0"} {
			got, cancel, err := svc.EventsAfter(ctx, alice, id, after)
			Expect(err).NotTo(HaveOccurred())
			ev := recv(got)
			Expect(ev.Kind).To(Equal(session.KindResync), after)
			Expect(ev.ID).To(BeEmpty(), "no run, so no head")
			cancel()
		}
		live, cancelLive, _ := svc.Events(ctx, alice, id)
		defer cancelLive()
		sent(svc, alice, id, "hello")
		for recv(live).Kind != session.KindText {
		}
		got, cancel, _ := svc.EventsAfter(ctx, alice, id, "other.3")
		defer cancel()
		ev := recv(got)
		Expect(ev.Kind).To(Equal(session.KindResync))
		Expect(ev.ID).ToNot(BeEmpty(), "the resync names where the stream is")
		h.End <- struct{}{}
		// The live events follow, numbered after the head.
		Expect(drain(got, session.KindTurnEnd)).ToNot(BeEmpty())
	})

	It("starts with a resync for an id ahead of the ring", func() {
		svc, h := heldService(nil)
		id := create(svc, alice)
		live, cancelLive, _ := svc.Events(ctx, alice, id)
		defer cancelLive()
		sent(svc, alice, id, "hello")
		var first session.Event
		for first.Kind != session.KindText {
			first = recv(live)
		}
		run := first.ID[:strings.LastIndex(first.ID, ".")]
		got, cancel, _ := svc.EventsAfter(ctx, alice, id, run+".9999")
		defer cancel()
		Expect(recv(got).Kind).To(Equal(session.KindResync))
		h.End <- struct{}{}
	})

	It("fills a gap in the live events from the ring", func() {
		bus := &lossyBus{Bus: studio.NewLocalBus()}
		bus.kind.Store(session.KindCheckpoint)
		svc, h := heldService(bus)
		id := create(svc, alice)
		live, cancelLive, _ := svc.Events(ctx, alice, id)
		defer cancelLive()
		sent(svc, alice, id, "hello")
		var first session.Event
		for first.Kind != session.KindText {
			first = recv(live)
		}
		got, cancel, _ := svc.EventsAfter(ctx, alice, id, first.ID)
		defer cancel()
		h.End <- struct{}{}
		// The checkpoint never reaches a subscriber. The numbers of text and
		// turn_end show the hole, and the ring has what is missing.
		events := drain(got, session.KindTurnEnd)
		var kinds []session.EventKind
		var ns []int
		for _, e := range events {
			kinds = append(kinds, e.Kind)
			ns = append(ns, idNumber(e.ID))
		}
		Expect(kinds).To(Equal([]session.EventKind{session.KindText, session.KindCheckpoint, session.KindTurnEnd}))
		Expect(ns[1]).To(Equal(ns[0] + 1))
		Expect(ns[2]).To(Equal(ns[1] + 1))
	})

	It("refuses a chat that is not the user's", func() {
		svc, _ := heldService(nil)
		id := create(svc, alice)
		_, _, err := svc.EventsAfter(ctx, "bob", id, "r.1")
		Expect(err).To(HaveOccurred())
	})
})

func idNumber(id string) int {
	n, err := strconv.Atoi(id[strings.LastIndex(id, ".")+1:])
	Expect(err).NotTo(HaveOccurred())
	return n
}
