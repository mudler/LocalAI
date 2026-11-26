// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The owner sets where a trigger reports on the trigger form, through the
// REST write of the trigger. The checks are the ones of update_trigger.
var _ = Describe("Chats trigger delivery through the REST API", func() {
	ctx := context.Background()
	var s *studioServer

	BeforeEach(func() {
		s = newStudioServer()
		s.svc.SetCapabilityGate(studio.CapabilityGateFunc(func(_ context.Context, r studio.CapabilityRequest) bool {
			return r.Capability == contract.CapTalkbackChannels
		}))
		bind := func(id, owner string, ch ...connectors.Channel) {
			// A binding with no agent holds credentials only and cannot receive a report.
			rec := s.do(owner, http.MethodPost, "/api/chats/agents", map[string]any{"name": "Helper", "model": "test-model"})
			Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
			Expect(s.connStore.PutBinding(ctx, connectors.Binding{ID: id, Owner: owner, Kind: "telegram", AddonID: "telegram", Name: "Bot " + id,
				AgentID: decodeJSON(rec)["id"].(string), Enabled: true, Token: []byte("SECRET-BOT-TOKEN"), Channels: ch})).To(Succeed())
		}
		bind("conn-a", "alice", connectors.Channel{ID: "-100", Label: "General", Approved: true}, connectors.Channel{ID: "-200", Label: "Ops"})
		bind("conn-b", "bob", connectors.Channel{ID: "-300", Label: "Bob room", Approved: true})
	})

	body := func(delivery map[string]any) map[string]any {
		b := map[string]any{"name": "Digest", "prompt": "Go.", "kind": "schedule", "schedule": "daily 07:30", "time_zone": "UTC",
			"enabled": true, "model": "test-model"}
		if delivery != nil {
			b["delivery"] = delivery
		}
		return b
	}
	create := func(delivery map[string]any) map[string]any {
		rec := s.do("alice", http.MethodPost, "/api/chats/triggers", body(delivery))
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)
	}
	deliveryOf := func(t map[string]any) map[string]any { return t["delivery"].(map[string]any) }

	It("creates a trigger that reports in an approved channel", func() {
		t := create(map[string]any{"target": "channel", "connection": "conn-a", "channel": "General", "mode": "on_change"})
		d := deliveryOf(t)
		Expect(d).To(HaveKeyWithValue("target", "channel"))
		Expect(d).To(HaveKeyWithValue("connection_id", "conn-a"))
		Expect(d).To(HaveKeyWithValue("channel_label", "General"))
		Expect(d).To(HaveKeyWithValue("mode", "on_change"))
		Expect(d).To(HaveKeyWithValue("state", "ok"))
		proposals := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/proposals", nil))["triggers"]
		Expect(proposals).To(BeEmpty(), "the owner's form saves at once, with no card")
	})

	It("refuses a foreign connection, a channel not approved and the origin with 400, and makes nothing", func() {
		for _, d := range []map[string]any{
			{"target": "channel", "connection": "conn-b", "channel": "-300"},
			{"target": "channel", "connection": "conn-a", "channel": "Ops"},
			{"target": "channel", "connection": "conn-a", "channel": "nowhere"},
			{"target": "origin"},
		} {
			rec := s.do("alice", http.MethodPost, "/api/chats/triggers", body(d))
			Expect(rec.Code).To(Equal(http.StatusBadRequest), "%v: %s", d, rec.Body.String())
			Expect(rec.Body.String()).NotTo(ContainSubstring("Bob room"))
		}
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers", nil))["triggers"]).To(BeEmpty())
	})

	It("moves the reports on PUT, keeps them when delivery is left out, and clears them back to the Inbox", func() {
		id := create(nil)["id"].(string)
		Expect(deliveryOf(create(nil))).To(HaveKeyWithValue("target", "inbox"))
		put := func(delivery map[string]any) (int, map[string]any) {
			rec := s.do("alice", http.MethodPut, "/api/chats/triggers/"+id, body(delivery))
			if rec.Code != http.StatusOK {
				return rec.Code, nil
			}
			return rec.Code, decodeJSON(rec)
		}
		code, t := put(map[string]any{"target": "channel", "connection": "conn-a", "channel": "-100"})
		Expect(code).To(Equal(http.StatusOK))
		Expect(deliveryOf(t)).To(HaveKeyWithValue("channel_label", "General"))

		code, t = put(nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(deliveryOf(t)).To(HaveKeyWithValue("target", "channel"), "no delivery field keeps the channel")

		code, _ = put(map[string]any{"target": "channel", "connection": "conn-b", "channel": "-300"})
		Expect(code).To(Equal(http.StatusBadRequest), "another owner's connection")
		code, _ = put(map[string]any{"target": "channel", "connection": "conn-a", "channel": "-200"})
		Expect(code).To(Equal(http.StatusBadRequest), "a channel not approved")
		Expect(deliveryOf(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+id, nil)))).To(HaveKeyWithValue("channel_label", "General"))

		code, t = put(map[string]any{"target": "inbox"})
		Expect(code).To(Equal(http.StatusOK))
		Expect(deliveryOf(t)).To(HaveKeyWithValue("target", "inbox"))
		Expect(deliveryOf(t)).NotTo(HaveKey("connection_id"))
	})

	It("lists the owner's destinations with the current channel, and nothing of another owner", func() {
		id := create(map[string]any{"target": "channel", "connection": "conn-a", "channel": "-100"})["id"].(string)
		rec := s.do("alice", http.MethodGet, "/api/chats/triggers/destinations?trigger="+id, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("SECRET-BOT-TOKEN"))
		Expect(rec.Body.String()).NotTo(ContainSubstring("conn-b"))
		conns := decodeJSON(rec)["connections"].([]any)
		Expect(conns).To(HaveLen(1))
		c := conns[0].(map[string]any)
		Expect(c).To(HaveKeyWithValue("id", "conn-a"))
		Expect(c["channels"]).To(Equal([]any{map[string]any{"id": "-100", "label": "General", "current": true}}), "only the approved channel")
		Expect(s.do("bob", http.MethodGet, "/api/chats/triggers/destinations?trigger="+id, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("", http.MethodGet, "/api/chats/triggers/destinations", nil).Code).To(Equal(http.StatusUnauthorized))
		Expect(s.do("dave", http.MethodGet, "/api/chats/triggers/destinations", nil).Code).To(Equal(http.StatusForbidden))
	})
})
