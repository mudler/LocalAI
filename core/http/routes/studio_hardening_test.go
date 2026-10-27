// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

var _ = Describe("Chats connectors: accepting any sender needs a server-checked confirmation", func() {
	var s *studioServer
	BeforeEach(func() {
		bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":555,"username":"spec_bot"}}`))
		}))
		DeferCleanup(bot.Close)
		studioTelegram = &connectors.Telegram{BaseURL: bot.URL, Client: bot.Client()}
		DeferCleanup(func() { studioTelegram = nil })
		s = newStudioServer()
	})

	It("refuses accept_any on create and update without the phrase, and takes it with the phrase", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Support", "model": "test-model"})
		Expect(rec.Code).To(Equal(http.StatusCreated))
		ag := decodeJSON(rec)["id"].(string)
		body := func(extra map[string]any) map[string]any {
			m := map[string]any{"name": "Bot", "token": specToken, "agent_id": ag, "enabled": true, "accept_any": true}
			for k, v := range extra {
				m[k] = v
			}
			return m
		}

		for _, extra := range []map[string]any{nil, {"confirm_accept_any": "yes"}, {"confirm_accept_any": ""}} {
			refused := s.do("alice", http.MethodPost, "/api/chats/connectors", body(extra))
			Expect(refused.Code).To(Equal(http.StatusBadRequest), refused.Body.String())
			Expect(refused.Body.String()).To(ContainSubstring("accept any message"))
		}
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors", nil))["connectors"]).To(BeEmpty(), "a refused request must create nothing")

		ok := s.do("alice", http.MethodPost, "/api/chats/connectors", body(map[string]any{"confirm_accept_any": "accept any message"}))
		Expect(ok.Code).To(Equal(http.StatusCreated), ok.Body.String())
		Expect(decodeJSON(ok)).To(HaveKeyWithValue("accept_any", true))
		id := decodeJSON(ok)["id"].(string)

		// Already on: other edits need no new confirmation. Turning it off needs none either.
		keep := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Renamed", "agent_id": ag, "enabled": true, "accept_any": true})
		Expect(keep.Code).To(Equal(http.StatusOK), keep.Body.String())
		off := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Renamed", "agent_id": ag, "enabled": true, "accept_any": false, "allow": []string{"42"}})
		Expect(off.Code).To(Equal(http.StatusOK))

		// Turning it on again is a new decision.
		again := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Renamed", "agent_id": ag, "enabled": true, "accept_any": true})
		Expect(again.Code).To(Equal(http.StatusBadRequest))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/connectors/"+id, nil))).To(HaveKeyWithValue("accept_any", false))
		confirmed := s.do("alice", http.MethodPut, "/api/chats/connectors/"+id, map[string]any{"name": "Renamed", "agent_id": ag, "enabled": true, "accept_any": true, "confirm_accept_any": "accept any message"})
		Expect(confirmed.Code).To(Equal(http.StatusOK), confirmed.Body.String())
	})
})
