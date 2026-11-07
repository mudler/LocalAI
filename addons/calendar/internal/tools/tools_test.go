// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/calendar/internal/store"
	"github.com/mudler/LocalAI/addons/calendar/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func env(user, action, scope string, args map[string]any) contract.Envelope {
	raw, _ := json.Marshal(args)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "calendar", OperationID: "op-1", Generation: 1, Action: action, Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "c1", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Hour)},
	}
}

var _ = Describe("calendar tools", func() {
	var (
		rt  *tools.Runtime
		ctx = context.Background()
	)
	read := func(user string, args map[string]any) map[string]any {
		GinkgoHelper()
		res, err := rt.Read(ctx, env(user, "read", "read", args))
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		return out
	}
	act := func(user string, args map[string]any) map[string]any {
		GinkgoHelper()
		res, err := rt.Act(ctx, env(user, "act", "act", args))
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
	})

	It("creates an event and lists the day it is on", func() {
		out := act("ann", map[string]any{"tool": "event-create", "title": "Dinner with Sam", "start": "2026-11-05T20:00"})
		Expect(out["ok"]).To(BeTrue())
		l := read("ann", map[string]any{"tool": "events-list"})
		Expect(l["today"]).To(Equal("2026-11-05"))
		Expect(l["zone"]).To(Equal("Europe/Rome"))
		evs := l["events"].([]any)
		Expect(evs).To(HaveLen(1))
		Expect(evs[0].(map[string]any)["weekday"]).To(Equal("Thursday"))
	})

	It("keeps each owner's calendar apart", func() {
		act("ann", map[string]any{"tool": "event-create", "title": "Mine", "start": "2026-11-05T20:00"})
		l := read("bob", map[string]any{"tool": "events-list"})
		Expect(l["events"]).To(BeEmpty())
		s := read("bob", map[string]any{"tool": "events-search", "query": "mine"})
		Expect(s["matches"]).To(BeEmpty())
	})

	It("changes and deletes an event, or one occurrence of a series", func() {
		c := act("ann", map[string]any{"tool": "event-create", "title": "Yoga", "start": "2026-11-05T18:00", "repeat": map[string]any{"freq": "weekly"}})
		id := c["event"].(map[string]any)["id"].(string)
		u := act("ann", map[string]any{"tool": "event-update", "id": id, "place": "Studio"})
		Expect(u["event"].(map[string]any)["place"]).To(Equal("Studio"))
		d := act("ann", map[string]any{"tool": "event-delete", "id": id, "occurrence": "2026-11-12"})
		Expect(d["skipped"]).To(Equal("2026-11-12"))
		l := read("ann", map[string]any{"tool": "events-list", "from": "2026-11-05", "to": "2026-11-20"})
		Expect(l["events"]).To(HaveLen(2))
		g := read("ann", map[string]any{"tool": "event-get", "id": id})
		Expect(g["skipped"]).To(ConsistOf("2026-11-12"))
		r := act("ann", map[string]any{"tool": "event-update", "id": id, "repeat": map[string]any{"freq": "none"}})
		Expect(r["event"].(map[string]any)).NotTo(HaveKey("repeat"))
		Expect(act("ann", map[string]any{"tool": "event-delete", "id": id})["ok"]).To(BeTrue())
		Expect(read("ann", map[string]any{"tool": "event-get", "id": id})["ok"]).To(BeFalse())
	})

	It("answers a mistake as a correctable refusal", func() {
		out := act("ann", map[string]any{"tool": "event-create", "title": "x", "start": "next thursday"})
		Expect(out["ok"]).To(BeFalse())
		Expect(out["error"]).To(ContainSubstring("YYYY-MM-DDTHH:MM"))
		out = act("ann", map[string]any{"tool": "event-create", "title": "x", "start": "2026-11-05T20:00", "colour": "red"})
		Expect(out["ok"]).To(BeFalse())
		out = act("ann", map[string]any{"tool": "event-update", "id": "ev-nope", "title": "y"})
		Expect(out["ok"]).To(BeFalse())
	})

	It("serves a write tool only through act, and a read tool only through read", func() {
		_, err := rt.Read(ctx, env("ann", "read", "read", map[string]any{"tool": "event-create", "title": "x", "start": "2026-11-05T20:00"}))
		Expect(err).To(HaveOccurred())
		_, err = rt.Act(ctx, env("ann", "act", "act", map[string]any{"tool": "events-list"}))
		Expect(err).To(HaveOccurred())
		_, err = rt.Read(ctx, env("ann", "read", "read", map[string]any{"tool": "nonsense"}))
		Expect(err).To(HaveOccurred())
	})

	It("does not take the owner from the arguments", func() {
		out := read("bob", map[string]any{"tool": "events-list", "owner": "ann"})
		Expect(out["ok"]).To(BeFalse())
	})

	It("serves the page: zone setting and export", func() {
		Expect(read("ann", map[string]any{"tool": "settings-set", "zone": "America/New_York"})["ok"]).To(BeTrue())
		Expect(read("ann", map[string]any{"tool": "settings-get"})["zone"]).To(Equal("America/New_York"))
		Expect(read("ann", map[string]any{"tool": "settings-set", "zone": "Mars/Base"})["ok"]).To(BeFalse())
		Expect(read("ann", map[string]any{"tool": "ui-event-create", "title": "A", "start": "2026-11-05T08:00"})["ok"]).To(BeTrue())
		x := read("ann", map[string]any{"tool": "events-export"})
		Expect(x["filename"]).To(Equal("calendar.ics"))
		Expect(x["ics"]).To(ContainSubstring("SUMMARY:A"))
	})
})
