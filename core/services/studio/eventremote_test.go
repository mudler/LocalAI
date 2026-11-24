package studio_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// replica is one frontend of a cluster: its own instance name, the shared
// store and the shared message bus.
func replica(net *testutil.FakeBus, st store.Store, instance string) (*studio.Service, *studiotest.GateHarness) {
	GinkgoHelper()
	h := studiotest.NewGateHarness()
	h.End = make(chan struct{}, 1)
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h,
		Bus: studio.NewBroadcastBus(net, nil), Replay: studio.NewReplayCarrier(net, nil), Instance: instance})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc, h
}

// firstText runs a turn on svc and returns its first text event, while the
// turn waits on h.End.
func firstText(svc *studio.Service, h *studiotest.GateHarness, id string) session.Event {
	GinkgoHelper()
	live, cancel, err := svc.Events(context.Background(), alice, id)
	Expect(err).NotTo(HaveOccurred())
	defer cancel()
	h.Gate <- struct{}{}
	sent(svc, alice, id, "hello")
	ev := recv(live)
	for ev.Kind != session.KindText {
		ev = recv(live)
	}
	return ev
}

var _ = Describe("chat event replay across frontends", func() {
	ctx := context.Background()
	var (
		net        *testutil.FakeBus
		st         store.Store
		a, b       *studio.Service
		ha         *studiotest.GateHarness
		id         string
		first      session.Event
		cancelAll  []func()
		eventsFrom = func(svc *studio.Service, after string) <-chan session.Event {
			GinkgoHelper()
			ch, cancel, err := svc.EventsAfter(ctx, alice, id, after)
			Expect(err).NotTo(HaveOccurred())
			cancelAll = append(cancelAll, cancel)
			return ch
		}
	)

	BeforeEach(func() {
		net = testutil.NewFakeBus()
		var err error
		st, err = store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		a, ha = replica(net, st, "instance-a")
		b, _ = replica(net, st, "instance-b")
		id = create(a, alice)
		first = firstText(a, ha, id)
		cancelAll = nil
		DeferCleanup(func() {
			for _, c := range cancelAll {
				c()
			}
		})
	})

	It("records the frontend that runs the turn as its owner", func() {
		info, err := st.Turn(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Owner).To(Equal("instance-a"))
		ha.End <- struct{}{}
		idle(a, alice, id)
	})

	It("serves a client on B the events that A's turn sent while it was away", func() {
		ha.End <- struct{}{}
		idle(a, alice, id)

		missed := drain(eventsFrom(b, first.ID), session.KindTurnEnd)
		var kinds []session.EventKind
		for _, e := range missed {
			Expect(e.ID).ToNot(BeEmpty())
			Expect(e.ID).ToNot(Equal(first.ID), "the event the client had was sent again")
			kinds = append(kinds, e.Kind)
		}
		Expect(kinds).To(ContainElements(session.KindText, session.KindCheckpoint, session.KindTurnEnd))
		Expect(kinds).ToNot(ContainElement(session.KindResync))
		Expect(missed[0].Text).To(Equal("done"))
	})

	It("goes on with the live events after the replay from A", func() {
		got := eventsFrom(b, first.ID)
		none(got)
		ha.End <- struct{}{}
		Expect(recv(got).Text).To(Equal("done"))
		Expect(drain(got, session.KindTurnEnd)).ToNot(BeEmpty())
	})

	It("sends a resync when the owner is gone", func() {
		ha.End <- struct{}{}
		idle(a, alice, id)
		Expect(a.Close(ctx)).To(Succeed()) // A stops answering
		ev := recv(eventsFrom(b, first.ID))
		Expect(ev.Kind).To(Equal(session.KindResync))
		Expect(ev.ID).To(BeEmpty(), "nobody knows the head")
	})

	It("sends a resync with A's head when the id is of an older run or ahead of the ring", func() {
		ha.End <- struct{}{}
		idle(a, alice, id)
		second := firstText(a, ha, id)
		run := first.ID[:strings.LastIndex(first.ID, ".")]
		for _, after := range []string{first.ID, run + ".9999"} {
			ev := recv(eventsFrom(b, after))
			Expect(ev.Kind).To(Equal(session.KindResync), after)
			Expect(ev.ID).To(HavePrefix(second.ID[:strings.LastIndex(second.ID, ".")]+"."), "the resync names where A's stream is")
		}
		ha.End <- struct{}{}
		idle(a, alice, id)
	})

	It("caps the event streams of one chat per user", func() {
		for range 8 {
			eventsFrom(b, "")
		}
		_, _, err := b.EventsAfter(ctx, alice, id, first.ID)
		Expect(err).To(MatchError(studio.ErrTooManyStreams))
		// The cap counts streams per chat: closing one frees a place.
		cancelAll[0]()
		eventsFrom(b, first.ID)
		ha.End <- struct{}{}
		idle(a, alice, id)
	})
})
