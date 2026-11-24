// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The old chat page kept its chats in the browser under localStorage
// "localai_chats_data". The page sends that value as it is.
var _ = Describe("Import of the chats kept in the browser", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	stored := func() map[string]any {
		call := `{"type":"tool_call","name":"search","arguments":{"q":"x"}}`
		result := `{"type":"tool_result","name":"search","result":"found it"}`
		return map[string]any{
			"activeChatId": "c1", "lastSaved": 1700000200000, "pendingDeletes": []any{},
			"chats": []any{
				map[string]any{"id": "c1", "name": "Trip plan", "model": "m1", "createdAt": 1700000000000, "updatedAt": 1700000100000,
					"history": []any{
						map[string]any{"role": "user", "content": "plan a trip"},
						map[string]any{"role": "thinking", "content": "hmm"},
						map[string]any{"role": "tool_call", "content": call},
						map[string]any{"role": "tool_result", "content": result},
						map[string]any{"role": "assistant", "content": "here you go"},
					}},
				map[string]any{"id": "c2", "name": "Second", "model": "m1", "createdAt": 1700000300000, "updatedAt": 1700000400000,
					"history": []any{map[string]any{"role": "user", "content": "hi"}}},
			},
		}
	}

	It("requires authentication and the chats feature", func() {
		Expect(s.do("", http.MethodPost, "/api/chats/import/browser", stored()).Code).To(Equal(http.StatusUnauthorized))
		Expect(s.do("carol", http.MethodPost, "/api/chats/import/browser", stored()).Code).To(Equal(http.StatusForbidden))
	})

	It("imports the chats of the user with their transcript, and a second upload adds nothing", func() {
		first := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/import/browser", stored()))
		Expect(first).To(HaveKeyWithValue("imported", BeNumerically("==", 2)))
		Expect(first).To(HaveKeyWithValue("skipped", BeNumerically("==", 0)))

		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats", nil))["conversations"].([]any)
		Expect(list).To(HaveLen(2))
		var id string
		for _, c := range list {
			if c.(map[string]any)["title"] == "Trip plan" {
				id = c.(map[string]any)["id"].(string)
			}
		}
		Expect(id).NotTo(BeEmpty())
		messages := s.do("alice", http.MethodGet, "/api/chats/"+id+"/messages", nil).Body.String()
		Expect(messages).To(ContainSubstring("plan a trip"))
		Expect(messages).To(ContainSubstring("result of search: found it"))

		again := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/import/browser", stored()))
		Expect(again).To(HaveKeyWithValue("imported", BeNumerically("==", 0)))
		Expect(again).To(HaveKeyWithValue("skipped", BeNumerically("==", 2)))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats", nil))["conversations"]).To(HaveLen(2))
	})

	It("keeps the chats of one user from another", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/import/browser", stored()).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats", nil))["conversations"]).To(BeEmpty())
		// What bob uploads is his own, though it has the same dates.
		Expect(decodeJSON(s.do("bob", http.MethodPost, "/api/chats/import/browser", stored()))).To(HaveKeyWithValue("imported", BeNumerically("==", 2)))
	})

	It("counts a chat it cannot use, and refuses a body that is not the stored form", func() {
		body := map[string]any{"chats": []any{
			map[string]any{"id": "x", "name": "No date", "history": []any{map[string]any{"role": "user", "content": "hi"}}},
			map[string]any{"id": "y", "name": "Empty", "createdAt": 1700000000000, "history": []any{}},
		}}
		out := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/import/browser", body))
		Expect(out).To(HaveKeyWithValue("imported", BeNumerically("==", 0)))
		Expect(out).To(HaveKeyWithValue("skipped", BeNumerically("==", 2)))
		Expect(s.do("alice", http.MethodPost, "/api/chats/import/browser", map[string]any{"nothing": 1}).Code).To(Equal(http.StatusBadRequest))
	})
})
