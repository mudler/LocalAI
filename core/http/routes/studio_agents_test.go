// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats agents endpoints", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/agents"),
		Entry("create", http.MethodPost, "/api/chats/agents"),
		Entry("read", http.MethodGet, "/api/chats/agents/a1"),
		Entry("update", http.MethodPut, "/api/chats/agents/a1"),
		Entry("delete", http.MethodDelete, "/api/chats/agents/a1"),
	)

	It("is refused to a user without the chats feature", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats/agents", nil).Code).To(Equal(http.StatusForbidden))
	})

	save := func(user, name string) map[string]any {
		rec := s.do(user, http.MethodPost, "/api/chats/agents", map[string]any{
			"name": name, "instructions": "Be brief.", "addons": []string{"office"}, "skills": []string{"review"},
		})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)
	}

	It("creates, reads, lists, replaces and deletes an agent", func() {
		a := save("alice", "Reviewer")
		id := a["id"].(string)
		Expect(a).To(HaveKeyWithValue("instructions", "Be brief."))
		Expect(a["mcp_servers"]).To(BeEmpty())
		Expect(a["collections"]).NotTo(BeNil())

		got := s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil)
		Expect(got.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(got)["addons"]).To(ConsistOf("office"))

		put := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Reviewer 2", "addons": []string{"sandbox"}})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(And(HaveKeyWithValue("name", "Reviewer 2"), HaveKeyWithValue("instructions", "")))

		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents", nil))
		Expect(list["agents"]).To(HaveLen(1))

		Expect(s.do("alice", http.MethodDelete, "/api/chats/agents/"+id, nil).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("keeps agents private to their owner", func() {
		id := save("alice", "Mine")["id"].(string)
		Expect(s.do("bob", http.MethodGet, "/api/chats/agents/"+id, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Taken over"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodDelete, "/api/chats/agents/"+id, nil).Code).To(Equal(http.StatusNotFound))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/agents", nil))["agents"]).To(BeEmpty())
		Expect(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil).Code).To(Equal(http.StatusOK))
	})

	It("refuses a bad definition with the reason", func() {
		for body, want := range map[string]string{
			`{"name":""}`:                            "name",
			`{"name":"a","addons":["Office Tools"]}`: "add-on",
			`{"name":"a","skills":[" "]}`:            "empty entry",
			`{"name":"a","model":"no-such-model"}`:   "unknown model",
		} {
			rec := s.do("alice", http.MethodPost, "/api/chats/agents", body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body)
			Expect(rec.Body.String()).To(ContainSubstring(want))
		}
		save("alice", "Dup")
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "dup"}).Code).To(Equal(http.StatusConflict))
	})

	It("writes exactly one JSON document when it refuses a body or a model", func() {
		for _, body := range []string{`{"name":"a","model":"no-such-model"}`, `{"name":`} {
			rec := s.do("alice", http.MethodPost, "/api/chats/agents", body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body)
			dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
			var first map[string]any
			Expect(dec.Decode(&first)).To(Succeed(), rec.Body.String())
			Expect(first).To(HaveKey("error"))
			var extra any
			Expect(dec.Decode(&extra)).To(Equal(io.EOF), "a second document follows: "+rec.Body.String())
		}
	})

	It("starts a chat with an agent, and only the user's own", func() {
		id := save("alice", "Helper")["id"].(string)
		rec := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "agent_id": id})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("agent_id", id))
		Expect(s.do("bob", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "agent_id": id}).Code).To(Equal(http.StatusNotFound))
	})

	It("takes the model of the agent when the request names none", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "With model", "model": "test-model"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := decodeJSON(rec)["id"].(string)
		chat := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"agent_id": id})
		Expect(chat.Code).To(Equal(http.StatusCreated), chat.Body.String())
		Expect(decodeJSON(chat)).To(HaveKeyWithValue("model", "test-model"))
	})
})
