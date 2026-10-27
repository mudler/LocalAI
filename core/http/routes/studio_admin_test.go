// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// An administrator sees what every user has set up, with its owner, and can
// switch off or delete it. The view never carries a secret.
var _ = Describe("Chats administrator view", func() {
	var (
		s                      *studioServer
		agentID, trigID, conID string
	)

	BeforeEach(func() {
		s = newStudioServer()
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Helper", "instructions": "PRIVATE-TEXT", "model": "test-model"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		agentID = decodeJSON(rec)["id"].(string)
		rec = s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{"name": "Digest", "prompt": "Go.", "kind": "schedule",
			"schedule": "daily 07:30", "time_zone": "UTC", "enabled": true, "model": "test-model"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		trigID = decodeJSON(rec)["id"].(string)
		rec = s.do("alice", http.MethodPost, "/api/chats/connectors", map[string]any{"name": "Bot", "token": specToken, "agent_id": agentID, "enabled": false})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		conID = decodeJSON(rec)["id"].(string)
	})

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/chats/admin/agents"},
		{http.MethodDelete, "/api/chats/admin/agents/x"},
		{http.MethodGet, "/api/chats/admin/triggers"},
		{http.MethodGet, "/api/chats/admin/triggers/x/runs"},
		{http.MethodPost, "/api/chats/admin/triggers/x/disable"},
		{http.MethodDelete, "/api/chats/admin/triggers/x"},
		{http.MethodGet, "/api/chats/admin/connectors"},
		{http.MethodPost, "/api/chats/admin/connectors/x/disable"},
		{http.MethodDelete, "/api/chats/admin/connectors/x"},
	}

	It("answers 401 without credentials and 403 to a user who is not an administrator", func() {
		for _, r := range routes {
			Expect(s.do("", r.method, r.path, nil).Code).To(Equal(http.StatusUnauthorized), r.method+" "+r.path)
			// alice owns the items and has every feature: she is still not an administrator.
			Expect(s.do("alice", r.method, r.path, nil).Code).To(Equal(http.StatusForbidden), r.method+" "+r.path)
			Expect(s.do("dave", r.method, r.path, nil).Code).To(Equal(http.StatusForbidden), r.method+" "+r.path)
		}
	})

	It("lists every user's agents, triggers and connectors with their owner and no secret", func() {
		agentsRec := s.do("root", http.MethodGet, "/api/chats/admin/agents", nil)
		Expect(agentsRec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(agentsRec)["agents"]).To(ConsistOf(And(HaveKeyWithValue("id", agentID), HaveKeyWithValue("owner", "alice"), HaveKeyWithValue("name", "Helper"))))
		Expect(agentsRec.Body.String()).NotTo(ContainSubstring("PRIVATE-TEXT"), "the owner's instructions stay private")

		trigRec := s.do("root", http.MethodGet, "/api/chats/admin/triggers", nil)
		Expect(trigRec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(trigRec)["triggers"]).To(ConsistOf(And(HaveKeyWithValue("id", trigID), HaveKeyWithValue("owner", "alice"), HaveKeyWithValue("enabled", true))))

		conRec := s.do("root", http.MethodGet, "/api/chats/admin/connectors", nil)
		Expect(conRec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(conRec)["connectors"]).To(ConsistOf(And(HaveKeyWithValue("id", conID), HaveKeyWithValue("owner", "alice"), HaveKeyWithValue("has_token", true))))
		Expect(conRec.Body.String()).NotTo(ContainSubstring(specToken))
		Expect(conRec.Body.String()).NotTo(ContainSubstring(`"token"`))
	})

	It("lists the runs of another user's trigger", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/triggers/"+trigID+"/run", nil).Code).To(Equal(http.StatusAccepted))
		rec := s.do("root", http.MethodGet, "/api/chats/admin/triggers/"+trigID+"/runs", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(rec)["runs"]).To(HaveLen(1))
		Expect(s.do("root", http.MethodGet, "/api/chats/admin/triggers/nope/runs", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("switches off and deletes what belongs to another user", func() {
		off := s.do("root", http.MethodPost, "/api/chats/admin/triggers/"+trigID+"/disable", nil)
		Expect(off.Code).To(Equal(http.StatusOK), off.Body.String())
		Expect(decodeJSON(off)).To(And(HaveKeyWithValue("enabled", false), HaveKeyWithValue("owner", "alice")))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+trigID, nil))).To(HaveKeyWithValue("enabled", false))

		Expect(s.do("alice", http.MethodPost, "/api/chats/connectors/"+conID+"/enable", nil).Code).To(Equal(http.StatusOK))
		coff := s.do("root", http.MethodPost, "/api/chats/admin/connectors/"+conID+"/disable", nil)
		Expect(coff.Code).To(Equal(http.StatusOK), coff.Body.String())
		Expect(decodeJSON(coff)).To(HaveKeyWithValue("enabled", false))

		Expect(s.do("root", http.MethodDelete, "/api/chats/admin/triggers/"+trigID, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/triggers/"+trigID, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("root", http.MethodDelete, "/api/chats/admin/connectors/"+conID, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/connectors/"+conID, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("root", http.MethodDelete, "/api/chats/admin/agents/"+agentID, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/agents/"+agentID, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("answers 404 for an id that does not exist", func() {
		for _, r := range []struct{ method, path string }{
			{http.MethodDelete, "/api/chats/admin/agents/nope"},
			{http.MethodPost, "/api/chats/admin/triggers/nope/disable"},
			{http.MethodDelete, "/api/chats/admin/triggers/nope"},
			{http.MethodPost, "/api/chats/admin/connectors/nope/disable"},
			{http.MethodDelete, "/api/chats/admin/connectors/nope"},
		} {
			Expect(s.do("root", r.method, r.path, nil).Code).To(Equal(http.StatusNotFound), r.method+" "+r.path)
		}
	})
})
