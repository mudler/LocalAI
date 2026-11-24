// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats dock endpoints", func() {
	var s *studioServer

	BeforeEach(func() {
		s = newStudioServerWith(newTestFiles(), nil)
	})

	dock := func(user, id string) map[string]any {
		rec := s.do(user, http.MethodGet, "/api/chats/"+id+"/dock", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		d, _ := decodeJSON(rec)["dock"].(map[string]any)
		return d
	}

	It("stores the dock with the chat, gives it with the chat, and clears it with no tab", func() {
		id := s.create("alice")
		Expect(dock("alice", id)).To(BeNil())
		Expect(s.detail("alice", id)).NotTo(HaveKey("dock"), "no dock until something opens one")

		body := map[string]any{"open": true, "width": 480, "active": 1, "tabs": []any{
			map[string]any{"page": "task-board"}, map[string]any{"page": "news", "folder": 7},
		}}
		rec := s.do("alice", http.MethodPut, "/api/chats/"+id+"/dock", body)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(dock("alice", id)).To(Equal(map[string]any{"open": true, "width": float64(480), "active": float64(1), "tabs": []any{
			map[string]any{"page": "task-board"}, map[string]any{"page": "news", "folder": float64(7)},
		}}))
		Expect(s.detail("alice", id)["dock"]).To(HaveKeyWithValue("width", BeNumerically("==", 480)))

		rec = s.do("alice", http.MethodPut, "/api/chats/"+id+"/dock", map[string]any{"open": false, "tabs": []any{}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(dock("alice", id)).To(BeNil())
	})

	It("refuses a dock out of its bounds, another user's chat and a revoked permission", func() {
		id := s.create("alice")
		for _, bad := range []any{
			"not json",
			map[string]any{"tabs": []any{map[string]any{"page": "Bad Name"}}},
			map[string]any{"tabs": []any{map[string]any{"page": "a"}}, "width": 200},
			map[string]any{"tabs": []any{map[string]any{"page": "a"}}, "active": 3},
			map[string]any{"tabs": []any{map[string]any{"page": "a"}, map[string]any{"page": "a"}}},
		} {
			rec := s.do("alice", http.MethodPut, "/api/chats/"+id+"/dock", bad)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), "%v: %s", bad, rec.Body.String())
		}
		ok := map[string]any{"open": true, "tabs": []any{map[string]any{"page": "a"}}}
		for _, m := range []string{http.MethodGet, http.MethodPut} {
			Expect(s.do("bob", m, "/api/chats/"+id+"/dock", ok).Code).To(Equal(http.StatusNotFound), m)
			Expect(s.do("carol", m, "/api/chats/"+id+"/dock", ok).Code).To(Equal(http.StatusForbidden), m)
		}
		Expect(dock("alice", id)).To(BeNil())
	})
})
