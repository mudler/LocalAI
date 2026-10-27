// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const specToken = "555:SPEC-secret-token"

var _ = Describe("Chats connectors endpoints", func() {
	var (
		s    *studioServer
		bot  *httptest.Server
		code int
	)
	BeforeEach(func() {
		code = http.StatusOK
		bot = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if code != http.StatusOK {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":555,"username":"spec_bot"}}`))
		}))
		DeferCleanup(bot.Close)
		studioTelegram = studiotest.NewBotTransport(&transport.API{BaseURL: bot.URL, Client: bot.Client()})
		DeferCleanup(func() { studioTelegram = nil })
		s = newStudioServer()
	})

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/connectors"),
		Entry("kinds", http.MethodGet, "/api/chats/connectors/kinds"),
		Entry("create", http.MethodPost, "/api/chats/connectors"),
		Entry("read", http.MethodGet, "/api/chats/connectors/c1"),
		Entry("update", http.MethodPut, "/api/chats/connectors/c1"),
		Entry("delete", http.MethodDelete, "/api/chats/connectors/c1"),
		Entry("enable", http.MethodPost, "/api/chats/connectors/c1/enable"),
		Entry("disable", http.MethodPost, "/api/chats/connectors/c1/disable"),
		Entry("test", http.MethodPost, "/api/chats/connectors/c1/test"),
		Entry("senders", http.MethodGet, "/api/chats/connectors/c1/senders"),
		Entry("allow", http.MethodPost, "/api/chats/connectors/c1/senders/42/allow"),
	)

	It("is refused to a user without the chats feature", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats/connectors", nil).Code).To(Equal(http.StatusForbidden))
	})

	agent := func(user, name, model string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/agents", map[string]any{"name": name, "model": model})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	create := func(user string, body map[string]any) *httptest.ResponseRecorder {
		return s.do(user, http.MethodPost, "/api/chats/connectors", body)
	}

	It("creates, reads, lists, switches, replaces and deletes a binding, and never returns the token", func() {
		ag := agent("alice", "Support", "test-model")
		rec := create("alice", map[string]any{"name": "Support bot", "token": specToken, "agent_id": ag, "enabled": true})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("SPEC-secret"))
		b := decodeJSON(rec)
		id := b["id"].(string)
		Expect(b).To(And(HaveKeyWithValue("has_token", true), HaveKeyWithValue("accept_any", false), HaveKeyWithValue("allow", BeEmpty())))

		for _, path := range []string{"/api/chats/connectors/" + id, "/api/chats/connectors"} {
			got := s.do("alice", http.MethodGet, path, nil)
			Expect(got.Code).To(Equal(http.StatusOK))
			Expect(got.Body.String()).NotTo(ContainSubstring("SPEC-secret"))
		}
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors", nil))["connectors"]).To(HaveLen(1))

		off := s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/disable", nil)
		Expect(decodeJSON(off)).To(HaveKeyWithValue("enabled", false))
		Expect(decodeJSON(s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/enable", nil))).To(HaveKeyWithValue("enabled", true))

		put := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Renamed", "agent_id": ag, "enabled": true, "allow": []string{"42", "@ada_l"}, "notify_chat": "7"})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(And(HaveKeyWithValue("name", "Renamed"), HaveKeyWithValue("has_token", true), HaveKeyWithValue("notify_chat", "7")))

		Expect(s.do("alice", http.MethodDelete, "/api/chats/connectors/"+id, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("lists the kinds that a running add-on serves and marks a binding whose add-on does not run", func() {
		kinds := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/kinds", nil))["kinds"].([]any)
		Expect(kinds).To(HaveLen(1))
		Expect(kinds[0]).To(And(HaveKeyWithValue("addon", "telegram"), HaveKeyWithValue("kind", "telegram"), HaveKeyWithValue("title", "Telegram")))

		ag := agent("alice", "Support", "test-model")
		made := decodeJSON(create("alice", map[string]any{"kind": "telegram", "name": "B", "token": specToken, "agent_id": ag}))
		Expect(made).To(And(HaveKeyWithValue("addon", "telegram"), HaveKeyWithValue("active", true)))

		studioTelegram.Running = false
		down := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/"+made["id"].(string), nil))
		Expect(down).To(And(HaveKeyWithValue("active", false), HaveKeyWithValue("reason", "install the Telegram add-on")))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/kinds", nil))["kinds"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodPost, "/api/chats/connectors/"+made["id"].(string)+"/test", nil).Code).To(Equal(http.StatusBadGateway))
		Expect(create("alice", map[string]any{"kind": "carrier-pigeon", "name": "x", "token": specToken, "agent_id": ag}).Code).To(Equal(http.StatusBadRequest))
	})

	It("keeps bindings private to their owner", func() {
		ag := agent("alice", "Support", "test-model")
		id := decodeJSON(create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag}))["id"].(string)
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/connectors/" + id},
			{http.MethodPut, "/api/chats/connectors/" + id},
			{http.MethodDelete, "/api/chats/connectors/" + id},
			{http.MethodPost, "/api/chats/connectors/" + id + "/enable"},
			{http.MethodPost, "/api/chats/connectors/" + id + "/test"},
			{http.MethodGet, "/api/chats/connectors/" + id + "/senders"},
			{http.MethodPost, "/api/chats/connectors/" + id + "/senders/1/allow"},
		} {
			Expect(s.do("bob", c.method, c.path, map[string]any{"name": "x", "agent_id": ag}).Code).To(Equal(http.StatusNotFound), c.method+" "+c.path)
		}
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/connectors", nil))["connectors"]).To(BeEmpty())
	})

	It("refuses a binding without an agent, with another user's agent, with an agent that has no model, or without a token", func() {
		ag := agent("alice", "Support", "test-model")
		bare := agent("alice", "Bare", "")
		bobs := agent("bob", "Bobs", "test-model")
		Expect(create("alice", map[string]any{"name": "x", "token": specToken}).Code).To(Equal(http.StatusBadRequest))
		Expect(create("alice", map[string]any{"name": "x", "token": specToken, "agent_id": bobs}).Code).To(Equal(http.StatusNotFound))
		no := create("alice", map[string]any{"name": "x", "token": specToken, "agent_id": bare})
		Expect(no.Code).To(Equal(http.StatusBadRequest))
		Expect(no.Body.String()).To(ContainSubstring("needs a model"))
		Expect(create("alice", map[string]any{"name": "x", "agent_id": ag}).Code).To(Equal(http.StatusBadRequest))
		bad := create("alice", map[string]any{"name": "x", "token": specToken, "agent_id": ag, "allow": []string{"not an id"}})
		Expect(bad.Code).To(Equal(http.StatusBadRequest))
	})

	It("tests the token against Telegram and says when it is refused", func() {
		ag := agent("alice", "Support", "test-model")
		id := decodeJSON(create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag}))["id"].(string)
		ok := s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/test", nil)
		Expect(ok.Code).To(Equal(http.StatusOK), ok.Body.String())
		Expect(decodeJSON(ok)).To(HaveKeyWithValue("bot_username", "spec_bot"))
		code = http.StatusUnauthorized
		bad := s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/test", nil)
		Expect(bad.Code).To(Equal(http.StatusBadGateway))
		Expect(bad.Body.String()).NotTo(ContainSubstring("SPEC-secret"))
	})

	It("lists no senders for a new binding and refuses to allow one that never wrote", func() {
		ag := agent("alice", "Support", "test-model")
		id := decodeJSON(create("alice", map[string]any{"name": "B", "token": specToken, "agent_id": ag}))["id"].(string)
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/"+id+"/senders", nil))["senders"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodPost, "/api/chats/connectors/"+id+"/senders/99/allow", nil).Code).To(Equal(http.StatusBadRequest))
	})
})
