// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/calendar/internal/store"
	"github.com/mudler/LocalAI/addons/calendar/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type emitted struct {
	op, typ, dedupe string
	event           map[string]any
}

type fakeEmitter struct {
	got []emitted
	err error
}

func (f *fakeEmitter) Emit(_ context.Context, op, typ string, payload any, dedupe string) error {
	raw, _ := json.Marshal(payload)
	var p struct {
		Event map[string]any `json:"event"`
	}
	_ = json.Unmarshal(raw, &p)
	f.got = append(f.got, emitted{op: op, typ: typ, dedupe: dedupe, event: p.Event})
	return f.err
}

var _ = Describe("the calendar's reports of changes", func() {
	var (
		rt  *tools.Runtime
		em  *fakeEmitter
		ctx = context.Background()
	)
	act := func(args map[string]any) map[string]any {
		GinkgoHelper()
		res, err := rt.Act(ctx, env("ann", "act", "act", args))
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		return out
	}
	page := func(args map[string]any) map[string]any {
		GinkgoHelper()
		res, err := rt.Read(ctx, env("ann", "read", "read", args))
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		return out
	}
	BeforeEach(func() {
		st, err := store.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		// Thursday 2026-11-05, 10:00 in Rome.
		st.Now = func() time.Time { return time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC) }
		DeferCleanup(func() { _ = st.Close() })
		Expect(st.SetZone(ctx, "ann", "Europe/Rome")).To(Succeed())
		rt = tools.New(st)
		em = &fakeEmitter{}
		rt.Events = em
	})

	It("reports an added event with its start as an instant, under the live call", func() {
		act(map[string]any{"tool": "event-create", "title": "Call with Lea", "start": "2026-11-06T09:30", "attendees": "Lea"})
		Expect(em.got).To(HaveLen(1))
		e := em.got[0]
		Expect(e.op).To(Equal("op-1"))
		Expect(e.typ).To(Equal(tools.EventCreated))
		Expect(e.event["title"]).To(Equal("Call with Lea"))
		Expect(e.event["next"]).To(Equal("2026-11-06T09:30:00+01:00"))
		Expect(e.event["zone"]).To(Equal("Europe/Rome"))
		Expect(e.event["attendees"]).To(Equal("Lea"))
	})

	It("reports a change and a removal from the page tools too, the removal with its title", func() {
		out := page(map[string]any{"tool": "ui-event-create", "title": "Dentist", "start": "2026-11-10T15:00"})
		id := out["event"].(map[string]any)["id"].(string)
		page(map[string]any{"tool": "ui-event-update", "id": id, "start": "2026-11-10T16:00"})
		page(map[string]any{"tool": "ui-event-delete", "id": id})
		Expect(em.got).To(HaveLen(3))
		Expect(em.got[1].typ).To(Equal(tools.EventUpdated))
		Expect(em.got[1].event["next"]).To(Equal("2026-11-10T16:00:00+01:00"))
		Expect(em.got[2].typ).To(Equal(tools.EventDeleted))
		Expect(em.got[2].event["title"]).To(Equal("Dentist"))
		Expect(em.got[2].event["id"]).To(Equal(id))
		Expect(em.got[0].dedupe).NotTo(Equal(em.got[1].dedupe))
	})

	It("gives a repeating event its next occurrence, and moves it on when a day is skipped", func() {
		out := act(map[string]any{"tool": "event-create", "title": "Standup", "start": "2026-11-05T09:30", "repeat": map[string]any{"freq": "daily", "until": "2026-11-07"}})
		id := out["event"].(map[string]any)["id"].(string)
		Expect(em.got[0].event["next"]).To(Equal("2026-11-06T09:30:00+01:00"), "today's has begun: the next is tomorrow")
		Expect(em.got[0].event["following"]).To(Equal([]any{"2026-11-07T09:30:00+01:00"}), "the later occurrences, for the host to move a reminder on")
		act(map[string]any{"tool": "event-delete", "id": id, "occurrence": "2026-11-06"})
		Expect(em.got[1].typ).To(Equal(tools.EventUpdated))
		Expect(em.got[1].event["next"]).To(Equal("2026-11-07T09:30:00+01:00"))
		act(map[string]any{"tool": "event-delete", "id": id, "occurrence": "2026-11-07"})
		Expect(em.got[2].typ).To(Equal(tools.EventDeleted), "no occurrence is left")
	})

	It("gives an all-day event the midnight of its day in its zone", func() {
		act(map[string]any{"tool": "event-create", "title": "Holiday", "start": "2026-11-08", "allDay": true})
		Expect(em.got[0].event["next"]).To(Equal("2026-11-08T00:00:00+01:00"))
		Expect(em.got[0].event["allDay"]).To(BeTrue())
		Expect(em.got[0].event).NotTo(HaveKey("following"), "an event that does not repeat has no later occurrence")
	})

	It("keeps the change when the report fails, and works with no host at all", func() {
		em.err = errors.New("host down")
		out := act(map[string]any{"tool": "event-create", "title": "Lunch", "start": "2026-11-05T13:00"})
		Expect(out["ok"]).To(BeTrue())
		rt.Events = nil
		out = act(map[string]any{"tool": "event-create", "title": "Tea", "start": "2026-11-05T16:00"})
		Expect(out["ok"]).To(BeTrue())
	})

	It("declares the paths it sends, so the host can read them", func() {
		raw, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		for _, typ := range []string{tools.EventCreated, tools.EventUpdated, tools.EventDeleted} {
			d, ok := m.Capabilities.FindEvent(typ)
			Expect(ok).To(BeTrue(), typ)
			Expect(d.Key).To(Equal("event.id"))
			Expect(d.Title).To(Equal("event.title"))
		}
		d, _ := m.Capabilities.FindEvent(tools.EventCreated)
		Expect(d.Anchor.Time).To(Equal("event.next"))
		Expect(d.Anchor.Zone).To(Equal("event.zone"))
		Expect(d.Anchor.AllDay).To(Equal("event.allDay"))
		Expect(d.Anchor.Following).To(Equal("event.following"))
		Expect(d.Anchor.Moves).To(ConsistOf("event.start", "event.end"), "a move must not count as a change beyond the time")
		Expect(m.Capabilities.HostOperations.Optional).To(ContainElement(contract.EventsEmitOp))
	})
})
