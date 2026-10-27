// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Saved agents, triggers and connectors are features of their own, off by
// default, so a user who may chat cannot schedule unattended runs or bind a
// public bot unless an administrator says so.
var _ = Describe("Chats automation permissions", func() {
	var s *studioServer

	BeforeEach(func() { s = newStudioServer() })

	groups := []struct {
		name  string
		list  string
		write []struct{ method, path string }
	}{
		{"agents", "/api/chats/agents", []struct{ method, path string }{
			{http.MethodPost, "/api/chats/agents"},
			{http.MethodGet, "/api/chats/agents/a1"},
			{http.MethodPut, "/api/chats/agents/a1"},
			{http.MethodDelete, "/api/chats/agents/a1"},
		}},
		{"triggers", "/api/chats/triggers", []struct{ method, path string }{
			{http.MethodPost, "/api/chats/triggers"},
			{http.MethodGet, "/api/chats/triggers/preview"},
			{http.MethodGet, "/api/chats/triggers/proposals"},
			{http.MethodPost, "/api/chats/triggers/proposals/p1/confirm"},
			{http.MethodDelete, "/api/chats/triggers/proposals/p1"},
			{http.MethodGet, "/api/chats/triggers/t1"},
			{http.MethodPut, "/api/chats/triggers/t1"},
			{http.MethodDelete, "/api/chats/triggers/t1"},
			{http.MethodPost, "/api/chats/triggers/t1/run"},
			{http.MethodPost, "/api/chats/triggers/t1/enable"},
			{http.MethodPost, "/api/chats/triggers/t1/disable"},
			{http.MethodGet, "/api/chats/triggers/t1/runs"},
		}},
		{"connectors", "/api/chats/connectors", []struct{ method, path string }{
			{http.MethodPost, "/api/chats/connectors"},
			{http.MethodGet, "/api/chats/connectors/c1"},
			{http.MethodPut, "/api/chats/connectors/c1"},
			{http.MethodDelete, "/api/chats/connectors/c1"},
			{http.MethodPost, "/api/chats/connectors/c1/enable"},
			{http.MethodPost, "/api/chats/connectors/c1/disable"},
			{http.MethodPost, "/api/chats/connectors/c1/test"},
			{http.MethodGet, "/api/chats/connectors/c1/senders"},
			{http.MethodPost, "/api/chats/connectors/c1/senders/s1/allow"},
		}},
	}

	for _, g := range groups {
		g := g
		It("answers 401, 403 and 200 on the "+g.name+" routes", func() {
			all := append([]struct{ method, path string }{{http.MethodGet, g.list}}, g.write...)
			for _, r := range all {
				Expect(s.do("", r.method, r.path, nil).Code).To(Equal(http.StatusUnauthorized), "no credentials: "+r.method+" "+r.path)
				// dave may chat but was not granted the feature.
				Expect(s.do("dave", r.method, r.path, nil).Code).To(Equal(http.StatusForbidden), "dave: "+r.method+" "+r.path)
				// carol has no chats at all, and no feature either.
				Expect(s.do("carol", r.method, r.path, nil).Code).To(Equal(http.StatusForbidden), "carol: "+r.method+" "+r.path)
			}
			for _, user := range []string{"alice", "root"} {
				Expect(s.do(user, http.MethodGet, g.list, nil).Code).To(Equal(http.StatusOK), user+" lists "+g.name)
			}
		})
	}

	It("keeps dave's plain chats working", func() {
		id := s.create("dave")
		Expect(s.do("dave", http.MethodGet, "/api/chats/"+id, nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("dave", http.MethodGet, "/api/chats", nil).Code).To(Equal(http.StatusOK))
	})

	It("refuses to start a chat with a saved agent without the agents feature", func() {
		ag := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Helper", "model": "test-model"}))["id"].(string)
		rec := s.do("dave", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "agent_id": ag})
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("agents"))
		// alice, who has the feature, can.
		Expect(s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "agent_id": ag}).Code).To(Equal(http.StatusCreated))
		// A plain chat needs no feature beyond chats.
		Expect(s.do("dave", http.MethodPost, "/api/chats", map[string]any{"model": "test-model"}).Code).To(Equal(http.StatusCreated))
	})
})
